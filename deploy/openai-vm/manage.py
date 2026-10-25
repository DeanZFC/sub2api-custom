#!/usr/bin/env python3
"""Initialize/check the optional stack or explicitly run its isolated Compose."""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import secrets
import socket
import stat
import subprocess
import sys
import urllib.error
import urllib.request

HERE = Path(__file__).resolve().parent
IMAGE = "ghcr.io/dofastted/vm2api:v1.3.225@sha256:420b5292047751e2b412b694176c71987320ccb132e09e3b51e0a11e8f19e23e"
SECRETS = ("VM2API_MASTER_KEY", "VM2API_DB_SECRET", "VM2API_ADMIN_PASSWORD", "VM2API_BRIDGE_SECRET")
REQUIRED = (*SECRETS, "OPENAI_VM_PROJECT", "OPENAI_VM_DATA_DIR", "VM2API_PORT", "VM2API_BRIDGE_LISTEN", "VM2API_BRIDGE_ORIGIN", "VM2API_BRIDGE_NAMESPACE", "BRIDGE_UID", "BRIDGE_GID")
RFC1918 = tuple(ipaddress.ip_network(n) for n in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"))


def fail(message):
    raise ValueError(message)


def listen_address(value):
    try:
        host, raw_port = value.rsplit(":", 1)
        address = ipaddress.IPv4Address(host)
        port = int(raw_port)
    except (ValueError, AttributeError):
        fail("Bridge listen must be a literal IPv4 address and port, such as 127.0.0.1:8097")
    if not (address.is_loopback or any(address in block for block in RFC1918)):
        fail("Bridge listen must be loopback or an explicit RFC1918 address; wildcard/public listeners are refused")
    if address.is_unspecified or not 1024 <= port <= 65535:
        fail("Bridge listen requires a non-wildcard address and a port from 1024 to 65535")
    return str(address), port


def load_env(path):
    if path.is_symlink() or not path.is_file():
        fail("Environment file must be a regular non-symlink file; run init.sh first")
    if stat.S_IMODE(path.stat().st_mode) != 0o600:
        fail("Environment file permissions must be 0600")
    values = {}
    for number, raw in enumerate(path.read_text().splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            fail(f"Malformed environment entry on line {number}")
        key, value = line.split("=", 1)
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*", key) or key in values:
            fail(f"Invalid or duplicate environment key on line {number}")
        if value.startswith("'") and value.endswith("'"):
            value = value[1:-1]
        if any(c in value for c in ("'", '"', "\r", "\n", "$", "\x00")):
            fail(f"Unsupported quoted/interpolated environment value on line {number}")
        values[key] = value
    validate_env(values)
    return values


def validate_env(values):
    missing = [key for key in REQUIRED if not values.get(key)]
    if missing:
        fail("Environment file is incomplete: " + ", ".join(missing))
    if not re.fullmatch(r"[a-z0-9][a-z0-9_-]{1,48}", values["OPENAI_VM_PROJECT"]):
        fail("Compose project name must contain 2-49 lowercase letters, numbers, underscores or hyphens")
    host, port = listen_address(values["VM2API_BRIDGE_LISTEN"])
    if values["VM2API_BRIDGE_ORIGIN"] != f"http://{host}:{port}":
        fail("Bridge origin must match the explicit private/loopback listener")
    if not values["VM2API_PORT"].isdigit():
        fail("VM2API_PORT must be numeric")
    control_port = int(values["VM2API_PORT"])
    if not 1024 <= control_port <= 65535 or control_port == port:
        fail("Control port must be from 1024 to 65535 and differ from the bridge port")
    data = Path(values["OPENAI_VM_DATA_DIR"])
    if not data.is_absolute() or str(data.resolve()) != str(data) or data == Path("/"):
        fail("Data directory must be a canonical absolute non-root path")
    for key in ("BRIDGE_UID", "BRIDGE_GID"):
        if not values[key].isdigit() or not 1 <= int(values[key]) <= 2147483647:
            fail(f"{key} must be a non-root positive numeric ID")
    for key in SECRETS:
        if len(values[key]) < 32 or values[key] != values[key].strip():
            fail(f"{key} must contain at least 32 characters without surrounding whitespace")
    if len(set(values[key] for key in SECRETS)) != len(SECRETS):
        fail("Use distinct secrets for the master key, database, admin password and bridge")
    if not re.fullmatch(r"[A-Za-z0-9_.-]{1,128}", values["VM2API_BRIDGE_NAMESPACE"]):
        fail("Namespace must be a stable installation identifier")


def private_dir(path, uid=None, gid=None):
    if path.is_symlink():
        fail(f"Refusing symlink directory: {path}")
    if path.exists():
        if not path.is_dir():
            fail(f"Expected directory: {path}")
        if uid is not None and (path.stat().st_uid != uid or path.stat().st_gid != gid):
            fail(f"Existing bridge state ownership differs from configured UID/GID: {path}; not changing it")
        return
    path.mkdir(parents=True, mode=0o700)
    os.chmod(path, 0o700)
    if uid is not None and os.geteuid() == 0:
        os.chown(path, uid, gid)


def initialize(args):
    env_path = args.env_file.expanduser().absolute()
    if env_path.exists() or env_path.is_symlink():
        values = load_env(env_path)
        requested = {"OPENAI_VM_PROJECT": args.project, "VM2API_BRIDGE_LISTEN": args.listen,
                     "VM2API_PORT": str(args.vm2api_port) if args.vm2api_port else None,
                     "OPENAI_VM_DATA_DIR": str(args.data_dir.expanduser().resolve()) if args.data_dir else None}
        for key, value in requested.items():
            if value is not None and value != values[key]:
                fail("Already initialized with different " + key + "; existing credentials and settings were preserved")
    else:
        host, port = listen_address(args.listen or "127.0.0.1:8097")
        uid = os.geteuid() or 10001
        gid = os.getegid() or 10001
        data_path = (args.data_dir or env_path.parent / "state").expanduser().resolve()
        if data_path.exists() and (not data_path.is_dir() or any(data_path.iterdir())):
            fail("Data already exists but this environment file is missing; restore the original .env instead of generating new secrets")
        values = {
            "OPENAI_VM_PROJECT": args.project or "sub2api-openai-vm",
            "OPENAI_VM_DATA_DIR": str(data_path),
            "VM2API_PORT": str(args.vm2api_port or 8787),
            "VM2API_BRIDGE_LISTEN": f"{host}:{port}",
            "VM2API_BRIDGE_ORIGIN": f"http://{host}:{port}",
            "VM2API_BRIDGE_NAMESPACE": "sub2api-" + secrets.token_hex(12),
            "BRIDGE_UID": str(uid), "BRIDGE_GID": str(gid),
            **{key: secrets.token_hex(32) for key in SECRETS},
        }
        validate_env(values)
        for value in values.values():
            if any(c in value for c in ("'", '"', "\r", "\n", "$", "\x00")):
                fail("Paths and settings cannot contain quotes, dollar signs or control characters")
        env_path.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(env_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as handle:
            handle.write("# Generated once by init.sh. Back up securely; never rotate while bindings exist.\n")
            for key, value in values.items():
                handle.write(f"{key}='{value}'\n")
            handle.flush()
            os.fsync(handle.fileno())
    data = Path(values["OPENAI_VM_DATA_DIR"])
    private_dir(data)
    private_dir(data / "vm2api")
    for suffix in ("vms", "data", "bin", "share", "src/config"):
        private_dir(data / "vm2api" / suffix)
    private_dir(data / "bridge")
    private_dir(data / "bridge/state", int(values["BRIDGE_UID"]), int(values["BRIDGE_GID"]))
    print(f"Initialized: {env_path} (0600; existing secrets/settings preserved)")
    print(f"Persistent data: {data}")
    print(f"Sub2API management and proxy origin: {values['VM2API_BRIDGE_ORIGIN']}")
    print("No services started. Run check.sh on the Linux amd64 deployment host.")


def command(argv, *, env=None, timeout=30):
    try:
        result = subprocess.run(argv, capture_output=True, text=True, env=env, timeout=timeout)
    except (FileNotFoundError, subprocess.TimeoutExpired):
        fail("Required command unavailable or timed out: " + argv[0])
    if result.returncode:
        # Compose output can include expanded secrets: never forward it.
        fail("Command failed: " + " ".join(argv[:3]) + " (output withheld to protect credentials)")
    return result.stdout


def compose_env(values):
    env = dict(os.environ)
    # Prevent ambient environment from overriding the reviewed .env settings.
    for key in values:
        env.pop(key, None)
    for key in ("COMPOSE_FILE", "COMPOSE_PROFILES", "COMPOSE_PROJECT_NAME", "COMPOSE_ENV_FILES"):
        env.pop(key, None)
    return env


def compose_config(env_path, values):
    raw = command(["docker", "compose", "--env-file", str(env_path), "-f", str(HERE / "compose.yaml"), "config", "--format", "json"], env=compose_env(values))
    config = json.loads(raw)
    services = config.get("services", {})
    if set(services) != {"vm2api", "bridge"}:
        fail("Unexpected services in optional Compose")
    control, bridge = services["vm2api"], services["bridge"]
    for service in services.values():
        if service.get("network_mode") != "host" or service.get("ports") or service.get("privileged"):
            fail("Unexpected network/privilege settings in optional Compose")
        if service.get("platform") != "linux/amd64":
            fail("This deployment is pinned to Linux amd64")
    if control["image"] != IMAGE or control["environment"].get("HOST") != "127.0.0.1":
        fail("Control image/listen address differs from the reviewed configuration")
    socket_targets = [v["target"] for v in control.get("volumes", [])]
    if socket_targets.count("/var/run/docker.sock") != 1:
        fail("Only the vm2api control service should receive the local Docker socket")
    if any(v["target"] == "/var/run/docker.sock" for v in bridge.get("volumes", [])):
        fail("Bridge must not receive a Docker socket")
    if not bridge.get("read_only") or bridge.get("user") != values["BRIDGE_UID"] + ":" + values["BRIDGE_GID"]:
        fail("Bridge must run as the configured non-root user with a read-only root filesystem")
    if bridge["environment"]["VM2API_MASTER_KEY"] != control["environment"]["VM2API_API_KEY"]:
        fail("Bridge and control-plane master keys differ")
    return config


def check_dirs(values):
    data = Path(values["OPENAI_VM_DATA_DIR"])
    for suffix in ("vm2api/vms", "vm2api/data", "vm2api/bin", "vm2api/share", "vm2api/src/config", "bridge/state"):
        path = data / suffix
        if not path.is_dir() or path.is_symlink():
            fail("Missing or symlinked persistent directory: " + str(path))
    state = data / "bridge/state"
    st = state.stat()
    if stat.S_IMODE(st.st_mode) != 0o700 or st.st_uid != int(values["BRIDGE_UID"]) or st.st_gid != int(values["BRIDGE_GID"]):
        fail("Bridge state must have mode 0700 and the configured UID/GID")


def get_json(url, bearer=None):
    request = urllib.request.Request(url)
    if bearer:
        request.add_header("Authorization", "Bearer " + bearer)
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=6) as response:
            return json.load(response)
    except (OSError, ValueError, urllib.error.URLError):
        fail("Health check failed at " + url + "; no credentials or response body printed")


def check(args):
    env_path = args.env_file.expanduser().absolute()
    values = load_env(env_path)
    check_dirs(values)
    config = compose_config(env_path, values)
    if not args.static:
        if platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64"):
            fail("Run host checks on a Linux amd64 host; Docker Desktop/ARM emulation is not a production target")
        sock = Path("/var/run/docker.sock")
        if not sock.exists() or not stat.S_ISSOCK(sock.stat().st_mode):
            fail("Expected local /var/run/docker.sock; remote/rootless Docker is outside this deployment")
        info = json.loads(command(["docker", "info", "--format", "{{json .}}"], timeout=15))
        if info.get("OSType") != "linux" or info.get("Architecture") not in ("x86_64", "amd64"):
            fail("Docker engine must be Linux amd64")
        context = json.loads(command(["docker", "context", "inspect"]))
        endpoint = os.environ.get("DOCKER_HOST") or context[0]["Endpoints"]["docker"]["Host"]
        if endpoint != "unix:///var/run/docker.sock":
            fail("Use the local rootful Docker engine at unix:///var/run/docker.sock")
        if not args.running:
            for host, port in (("127.0.0.1", int(values["VM2API_PORT"])), listen_address(values["VM2API_BRIDGE_LISTEN"])):
                try:
                    with socket.socket() as probe:
                        probe.bind((host, port))
                except OSError:
                    fail(f"Listen address unavailable or port already in use: {host}:{port}; use --running after startup")
        else:
            control_health = get_json(f"http://127.0.0.1:{values['VM2API_PORT']}/healthz")
            bridge_health = get_json(values["VM2API_BRIDGE_ORIGIN"] + "/v1/health", values["VM2API_BRIDGE_SECRET"])
            if control_health.get("status") != "ok" or bridge_health.get("status") != "ok":
                fail("Health response did not confirm status ok")
    if args.static:
        print("Static checks passed: configuration, paths, ownership, image pin and Compose permissions.")
    elif args.running:
        print("Checks passed: host, configuration, control liveness and authenticated bridge health.")
        print("No slot created; OAuth, inference, runtime compatibility and egress remain separate acceptance checks.")
    else:
        print("Preflight passed: Linux amd64, local Docker, configuration, paths and listener availability.")
    print("Control plane: loopback only; bridge: " + values["VM2API_BRIDGE_ORIGIN"])
    return config


def run_compose(args):
    env_path = args.env_file.expanduser().absolute()
    values = load_env(env_path)
    # The same sanitized environment is used by check.sh and real operations.
    argv = ["docker", "compose", "--env-file", str(env_path), "-f", str(HERE / "compose.yaml"), *args.compose_args]
    os.execvpe("docker", argv, compose_env(values))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    init = commands.add_parser("init")
    init.add_argument("--env-file", type=Path, default=HERE / ".env")
    init.add_argument("--data-dir", type=Path)
    init.add_argument("--project")
    init.add_argument("--listen", help="Explicit loopback/RFC1918 IPv4:port; default 127.0.0.1:8097")
    init.add_argument("--vm2api-port", type=int)
    check_parser = commands.add_parser("check")
    check_parser.add_argument("--env-file", type=Path, default=HERE / ".env")
    mode = check_parser.add_mutually_exclusive_group()
    mode.add_argument("--static", action="store_true", help="No engine, platform, listener or HTTP probes")
    mode.add_argument("--running", action="store_true", help="Probe existing services without provisioning")
    compose_parser = commands.add_parser("compose", help="Run this optional stack with its saved environment")
    compose_parser.add_argument("--env-file", type=Path, default=HERE / ".env")
    compose_parser.add_argument("compose_args", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    try:
        if args.command == "init":
            initialize(args)
        elif args.command == "check":
            check(args)
        else:
            run_compose(args)
    except (ValueError, OSError, KeyError, json.JSONDecodeError) as error:
        print("ERROR: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

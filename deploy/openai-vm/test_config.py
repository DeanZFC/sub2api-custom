#!/usr/bin/env python3
"""Static deployment regression tests. No Docker daemon or real credentials needed."""
import importlib.util
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("vm_deploy", HERE / "manage.py")
vm_deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vm_deploy)


class DeploymentConfigTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="openai-vm-deploy-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = self.root / ".env"

    def run_cli(self, command, *args, expected=0, environment=None):
        result = subprocess.run(["python3", str(HERE / "manage.py"), command, "--env-file", str(self.env), *args],
                                capture_output=True, text=True, env=environment)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def initialize(self, *args):
        return self.run_cli("init", *args)

    def test_initialization_is_private_random_and_idempotent(self):
        first = self.initialize()
        original = self.env.read_bytes()
        values = vm_deploy.load_env(self.env)
        second = self.initialize()
        self.assertEqual(self.env.read_bytes(), original)
        self.assertEqual(stat.S_IMODE(self.env.stat().st_mode), 0o600)
        self.assertEqual(len({values[key] for key in vm_deploy.SECRETS}), 4)
        for key in vm_deploy.SECRETS:
            self.assertEqual(len(values[key]), 64)
            self.assertNotIn(values[key], first.stdout + first.stderr + second.stdout + second.stderr)
        state = Path(values["OPENAI_VM_DATA_DIR"]) / "bridge/state"
        self.assertEqual(stat.S_IMODE(state.stat().st_mode), 0o700)
        self.assertEqual(state.stat().st_uid, int(values["BRIDGE_UID"]))

    def test_configuration_changes_do_not_replace_existing_secrets(self):
        self.initialize()
        original = self.env.read_bytes()
        self.run_cli("init", "--listen", "172.18.0.1:8097", expected=1)
        self.assertEqual(self.env.read_bytes(), original)

    def test_new_project_has_different_secrets_and_namespace(self):
        self.initialize()
        first = vm_deploy.load_env(self.env)
        self.env = self.root / "second" / ".env"
        self.initialize()
        second = vm_deploy.load_env(self.env)
        for key in (*vm_deploy.SECRETS, "VM2API_BRIDGE_NAMESPACE"):
            self.assertNotEqual(first[key], second[key])

    def test_rejects_wildcard_public_and_reserved_listeners_without_writing(self):
        for address in ("0.0.0.0:8097", "8.8.8.8:8097", "169.254.1.1:8097", "192.0.2.1:8097", "localhost:8097", "127.0.0.1:80"):
            with self.subTest(address=address):
                self.run_cli("init", "--listen", address, expected=1)
                self.assertFalse(self.env.exists())

    def test_accepts_explicit_docker_gateway_and_space_in_data_path(self):
        self.initialize("--listen", "172.18.0.1:8097", "--data-dir", str(self.root / "persistent data"))
        values = vm_deploy.load_env(self.env)
        self.assertEqual(values["VM2API_BRIDGE_ORIGIN"], "http://172.18.0.1:8097")
        self.run_cli("check", "--static")

    def test_rejects_insecure_environment_permissions(self):
        self.initialize()
        original = self.env.read_bytes()
        os.chmod(self.env, 0o644)
        self.run_cli("init", expected=1)
        self.assertEqual(self.env.read_bytes(), original)

    def test_rejects_environment_symlinks(self):
        target = self.root / "untouched"
        target.write_text("not an environment file")
        self.env.symlink_to(target)
        self.run_cli("init", expected=1)
        self.assertEqual(target.read_text(), "not an environment file")

    def test_missing_environment_cannot_rekey_existing_data(self):
        state = self.root / "state"
        state.mkdir()
        account = state / "existing-account.json"
        account.write_text("encrypted-state-placeholder")
        self.run_cli("init", expected=1)
        self.assertFalse(self.env.exists())
        self.assertEqual(account.read_text(), "encrypted-state-placeholder")

    def test_compose_is_pinned_and_has_no_public_control_or_bridge_socket(self):
        self.initialize()
        values = vm_deploy.load_env(self.env)
        config = vm_deploy.compose_config(self.env, values)
        control = config["services"]["vm2api"]
        bridge = config["services"]["bridge"]
        self.assertEqual(control["image"], vm_deploy.IMAGE)
        self.assertEqual(control["environment"]["HOST"], "127.0.0.1")
        self.assertNotIn("ports", control)
        self.assertNotIn("ports", bridge)
        self.assertEqual(bridge["cap_drop"], ["ALL"])
        self.assertIn("no-new-privileges:true", bridge["security_opt"])
        self.assertEqual(bridge["tmpfs"], ["/tmp:rw,noexec,nosuid,size=16m"])
        self.assertEqual(bridge["healthcheck"]["test"], ["CMD", "wget", "-q", "-O", "/dev/null", "-T", "5", values["VM2API_BRIDGE_ORIGIN"] + "/health"])
        self.assertTrue(all(not v.get("bind", {}).get("create_host_path", True) for v in control["volumes"]))
        self.assertEqual([v["target"] for v in bridge["volumes"]], ["/var/lib/vm2api-bridge"])

    def test_ambient_environment_cannot_replace_checked_credentials(self):
        self.initialize()
        env = dict(os.environ, VM2API_MASTER_KEY="should-not-override", VM2API_BRIDGE_LISTEN="0.0.0.0:8097", COMPOSE_FILE="/does/not/exist")
        self.run_cli("check", "--static", environment=env)
        result = self.run_cli("compose", "config", "--services", environment=env)
        self.assertEqual(set(result.stdout.splitlines()), {"vm2api", "bridge"})

    def test_missing_state_fails_without_creating_it(self):
        self.initialize()
        values = vm_deploy.load_env(self.env)
        state = Path(values["OPENAI_VM_DATA_DIR"]) / "bridge/state"
        state.rmdir()
        self.run_cli("check", "--static", expected=1)
        self.assertFalse(state.exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)

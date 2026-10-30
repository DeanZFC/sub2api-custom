#!/usr/bin/env python3
"""Refresh fixed IPdeny CN sources after validating both address families."""

import hashlib
import ipaddress
import os
from pathlib import Path
import tempfile
import time
from datetime import datetime, timezone
from urllib.request import Request, urlopen


SOURCES = (
    (4, "https://www.ipdeny.com/ipblocks/data/aggregated/cn-aggregated.zone"),
    (6, "https://www.ipdeny.com/ipv6/ipaddresses/aggregated/cn-aggregated.zone"),
)
COPYRIGHT_URL = "https://www.ipdeny.com/ipblocks/data/countries/Copyrights.txt"
MAX_BYTES = 16 << 20


def download(url):
    request = Request(url, headers={"User-Agent": "Sub2API-offline-country-data/1.0"})
    with urlopen(request, timeout=30) as response:
        body = response.read(MAX_BYTES + 1)
    if not body or len(body) > MAX_BYTES:
        raise ValueError(f"Empty or oversized response: {url}")
    return body


def replace_atomic(path, content):
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
            temporary = Path(handle.name)
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        temporary.chmod(0o644)
        os.replace(temporary, path)
        temporary = None
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def main():
    downloaded = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    lines = [
        "# Mainland China country allocation prefixes from IPdeny.com.",
        "# Downloaded UTC: " + downloaded,
        "# Distribution requires the accompanying unchanged Copyrights.txt.",
        "# Registry country allocation data is not authoritative physical geolocation.",
    ]
    counts = []
    for family, url in SOURCES:
        body = download(url)
        records = body.decode("ascii").splitlines()
        prefixes = [ipaddress.ip_network(record.strip(), strict=True)
                    for record in records if record.strip()]
        if len(prefixes) < 100:
            raise ValueError(f"Incomplete IPv{family} data: {len(prefixes)} prefixes")
        if any(prefix.version != family or prefix.prefixlen == 0 for prefix in prefixes):
            raise ValueError(f"Wrong address family or default route in {url}")
        lines.extend([
            f"# IPv{family} source: {url}",
            "# Source SHA-256: " + hashlib.sha256(body).hexdigest(),
            "# Source prefix count: " + str(len(prefixes)),
            *records,
        ])
        counts.append(len(prefixes))
        time.sleep(0.75)
    notice = download(COPYRIGHT_URL)
    if b"IPDENY.COM COPYRIGHT NOTICE" not in notice or len(notice) < 500:
        raise ValueError("Invalid upstream redistribution notice")
    dataset = ("\n".join(lines) + "\n").encode("ascii")
    if len(dataset) > MAX_BYTES:
        raise ValueError("Combined dataset exceeds the loader size limit")
    output = Path(__file__).resolve().parent / "data"
    output.mkdir(exist_ok=True)
    # Validate every download before replacing either existing file. The
    # copyright notice is replaced first to accompany newly published data.
    replace_atomic(output / "Copyrights.txt", notice)
    replace_atomic(output / "cn.zone", dataset)
    print(f"Updated {downloaded}: {counts[0]} IPv4 and {counts[1]} IPv6 prefixes.")
    print("Review changes and run Go tests before rebuilding or installing the data.")


if __name__ == "__main__":
    main()

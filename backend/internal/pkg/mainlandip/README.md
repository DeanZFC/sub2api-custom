# Mainland country IP data

This package performs offline IPv4 and IPv6 membership checks for the `CN`
country allocation blocks published by [IPdeny](https://www.ipdeny.com/). It
does not call an IP lookup service, retain visitor addresses, or download data
while handling requests. `Default` parses the embedded data once; `Load` accepts
a local replacement file with the same format. Both reject incomplete files
that lack either address family.

## Provenance and limitations

Bundled download: **2026-10-30T07:59:01Z**. Original prefix counts: **5,517
IPv4** and **2,036 IPv6**. The single `data/cn.zone` file records its source URLs,
download time, and the SHA-256 of each original response.

- IPv4: <https://www.ipdeny.com/ipblocks/data/aggregated/cn-aggregated.zone>
- IPv6: <https://www.ipdeny.com/ipv6/ipaddresses/aggregated/cn-aggregated.zone>

The upstream data derives from regional Internet registries. Its country is
where addresses were allocated or assigned; it is **not authoritative physical
geolocation**. Address transfers, outdated allocations and remote access can
produce false positives or false negatives. This is a practical regional access
filter, not a way to prove a visitor's nationality or current location.

## Refresh

From this package directory, run `python3 update_data.py`. The helper downloads
the two fixed HTTPS sources and original copyright notice, validates every CIDR
and both address families, records response hashes, then atomically replaces
each output. A download or validation failure leaves the previous dataset
untouched. Review the diff, then run `go test -race ./internal/pkg/mainlandip`
from `backend/` before rebuilding a release. Existing binaries keep their
embedded snapshot until rebuilt; a configured external file is read at startup
and requires a restart after replacement. There is no runtime auto-update.

Refresh periodically or when the operator needs newer coverage. Respect
IPdeny's fair usage limits: at most five concurrent connections and 5,000 zone
downloads per IP per day, with a suggested 0.5–1 second between requests. This
helper downloads sequentially with a 0.75 second interval.

## Redistribution notice

[IPdeny's copyright policy](https://www.ipdeny.com/copyright.php) explicitly
permits redistributing its zone files provided the original `Copyrights.txt`
accompanies them intact. `data/Copyrights.txt` is the unmodified upstream notice
from <https://www.ipdeny.com/ipblocks/data/countries/Copyrights.txt> and is embedded
alongside the data so it remains part of compiled distributions. Preserve that
file when redistributing this package or its country data. The notice includes
upstream registry conditions and forbids use for spam, bulk advertising and
illegal activities. See also [IPdeny usage limits](https://www.ipdeny.com/usagelimits.php).

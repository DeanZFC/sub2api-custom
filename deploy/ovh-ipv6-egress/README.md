> The bundled config is a template: replace the documentation-only `2001:db8::/32`
> prefix with your own routed prefix, choose your network interface and private listen addresses,
> and generate your own control token (at least 32 characters). Set the same value in
> `OVH_IPV6_CONTROL_TOKEN_FILE` (a mounted secret file) for Sub2API; set `OVH_IPV6_CONTROL_URL` to your control listener.
> No deployable control credential is included in this source package.

# OVH IPv6 egress agent

The agent is a private HTTP proxy on the dedicated host. Sub2API reaches it on
the Docker bridge's IPv4 gateway; every upstream connection uses the lease's
fixed IPv6 source and an AAAA destination. HTTP CONNECT keeps upstream TLS
end-to-end. Only public IPv6 destinations on ports 80 and 443 are accepted.
Configure exact lowercase `allowed_hosts` and explicit `allowed_clients` CIDRs;
both lists are mandatory. Client matching uses the socket peer, never forwarded
headers. Add any non-OpenAI provider hostname explicitly before using its accounts
or audit keys. The probe hostname must also appear in `allowed_hosts`.

Build `./cmd/ipv6-egress` from the same committed backend source as Sub2API.
Install the Linux binary at `/usr/local/lib/sub2api-ovh-ipv6/ipv6-egress` and the unit at
`/etc/systemd/system/sub2api-ovh-ipv6-egress.service`. The host needs `iproute2`.
The service requires host networking and `CAP_NET_ADMIN`; it does not need
Docker control, database credentials, OpenAI tokens, or TLS private keys.

Copy `config.example.json` to `/etc/sub2api-ovh-ipv6-egress/config.json`, mode `0600`.
Replace the control token with at least 32 random characters. Verify the actual
public interface, assigned prefix and Docker bridge gateway before installation.
Do not expose either listener through public port mappings. Both listeners
refuse wildcard/public bind addresses and authenticate every request.

For a new allocation store only:

```sh
/usr/local/lib/sub2api-ovh-ipv6/ipv6-egress --config /etc/sub2api-ovh-ipv6-egress/config.json --initialize
systemctl daemon-reload
systemctl enable --now sub2api-ovh-ipv6-egress
```

Initialization exits after writing the empty store. It refuses existing or
partially existing state. Normal startup refuses missing state and mismatched
prefixes; it never reconstructs used addresses from the Sub2API database.

Set these private environment variables on the Sub2API application slots:

```text
OVH_IPV6_CONTROL_URL=http://172.22.0.1:11881
OVH_IPV6_CONTROL_TOKEN_FILE=/run/secrets/ovh-ipv6-control-token
```

The token file takes precedence over the legacy `OVH_IPV6_CONTROL_TOKEN` value.
An unreadable or invalid configured file fails closed; it never falls back to
the environment value. The URL must be an HTTP(S) origin without credentials,
path, query or fragment. The client never follows control API redirects.

The original fixed IPv6 daemon in `tools/ipv6-egress` remains separate. Do not
replace its binary, reuse its state, or overlap its allocation prefix. See
`docs/ovh-ipv6-compatibility.md` before installing the second provider.

For the application's optional storage-usage view, also set
`OVH_IPV6_STATE_STATS_DIR=/run/ovh-ipv6-state` and bind-mount the entire host state
directory read-only at that path. Do not bind individual files: atomic renames
would leave those mounts pointing at stale inodes. Grant the actual application
UID directory traversal only (for example, `setfacl -m u:1000:--x
/var/lib/sub2api-ovh-ipv6-egress`), retaining root ownership and `0600` files. The app
only stats the three fixed regular files and never reads their contents. Verify
permissions as the application UID and restore/check the traverse ACL after
future systemd starts, which may reset its mask via `StateDirectoryMode=0700`.
The existing agent protocol and process are unchanged; only new application
slots need the mount. See `docs/ovh-ipv6-compatibility.md` for API fields and size scope.

## Control protocol

All endpoints require `Authorization: Bearer <control token>`. JSON bodies are
bounded to 4 KiB. Errors never include proxy passwords or account credentials.

| Request | Response |
| --- | --- |
| `GET /v1/status` | Configured/available status, prefix, decimal `issued` counter, active and pending-cleanup counts |
| `POST /v1/leases` | New address, decimal ID, `ovh6_<ID>` username, random password, proxy host/port, `state: pending` |
| `POST /v1/leases/<ID>/activate` | Idempotently activates a committed binding; retired IDs fail |
| `DELETE /v1/leases/<ID>` | Immediately denies new connections and returns `retiring` until existing streams drain, then `retired` |

Creation configures `/128`, waits for duplicate-address detection, and verifies
an HTTPS connection using that source. A failed attempt burns its sequence and
cleans up its address. The caller must activate after its database binding
commits. Unclaimed allocations expire after 15 minutes. The database worker must
keep retrying pending activation and retirement; a stale restored database must
never substitute direct routing for an unavailable lease.

Every lease is added with `preferred_lft 0 valid_lft forever`. Linux marks it
deprecated, preserving the host's existing preferred IPv6 for ordinary source
selection; the proxy can still explicitly bind the lease as its TCP source.
Restoring an existing owned `/128` reapplies these lifetimes with `ip addr change`.
DAD stays enabled, and the agent never changes the primary address or routes.
Deployment validation must confirm the unbound route's source before and after
allocation, as well as an explicit-source connection through the new lease.

The proxy username/password are entered in Sub2API's ordinary HTTP proxy fields.
An active lease is immutable; switch an account by creating a new lease, binding
it, and retiring the old one. Deleting only an account shadow must not retire its
parent's lease.

The application also supports explicit `proxy_mode: ovh_ipv6` and administrator
allocation of one unbound pool entry. Such reserved entries activate after their
database transaction commits and remain active until first binding or deletion.
They use the existing agent protocol; see `docs/ovh-ipv6-compatibility.md` for application
permissions and lifecycle rules. The global random-proxy toggle does not gate
explicit allocation.

## Durable state and recovery

`counter.json` and `counter.guard.json` hold the same monotonic decimal counter.
Both are atomically replaced and fsynced before configuring a newly issued
address. On restart the greater valid counter wins, preventing reuse after a
partial counter restore. `leases.json` contains only pending, active and
retiring leases, including their proxy credentials; completed retirement drops
the lease entirely. A Linux advisory lock prevents two agents using one store.

Back up all three files securely, independently of the application database.
Never restore both counters to an older value. An intentionally full host
restore must reconcile the largest known issued counter from the latest state
backup before starting the allocator. Loss of every current counter copy cannot
be repaired by guessing from surviving accounts. Leave the allocator stopped
until the correct high-water mark is recovered.

Existing leases are restored with the same addresses and credentials after a
host reboot. Retiring leases are never restored as active. Cleanup failures keep
the lease in `retiring` and retry every two seconds. Pending and retired leases
cannot become active after their retirement decision.

The proxy waits for active CONNECT tunnels to finish during graceful service
shutdown; the supplied unit has no forced stop timeout. A host reboot, explicit
SIGKILL or hardware failure still breaks network connections. For an application
blue/green update, keep the agent running and change only Sub2API slots.

## Verification

```sh
go test -race ./internal/ipv6egress ./cmd/ipv6-egress
```

Tests cover unique concurrent allocation, failed-allocation consumption,
retirement without per-address history, restart and partial-counter recovery,
orphan expiry, cleanup retries, connection draining, authenticated control,
HTTP credential isolation, fixed source selection and refusal of IPv4/private
destinations. Command tests verify deprecated lifetimes, DAD waiting and primary
address protection. The Linux integration test
`TestLinuxDeprecatedLeasePreservesDefaultAndAllowsBind` additionally checks the
kernel's selected source and a TCP connection explicitly bound to a deprecated
lease. It requires the `integration` build tag, `IPV6_EGRESS_KERNEL_TEST=1`,
`iproute2` and `CAP_NET_ADMIN`; run it only inside a disposable container network
namespace. Production validation should use two newly allocated addresses,
confirm their visible IPv6 exits, and retire both after the check.

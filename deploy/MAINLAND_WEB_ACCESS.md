# Mainland web access restriction

Sub2API can return a built-in regional access notice for mainland China IPs
without configuring a CDN rule. One domain is enough. The response is HTTP 403
with a responsive, self-contained Chinese/English page, light/dark appearance,
and no third-party assets or requests.

## Exact scope

Only `/`, `/login` and `/home` are matched, regardless of query parameters.
GET and HEAD document requests are protected; OPTIONS retains the existing
CORS handling. Trailing-slash paths, `/register`, other pages and all API routes
are unaffected. This is deliberately a restriction on these three HTTP paths,
not a guarantee that users cannot load the SPA through another route or log
in through authentication APIs. The feature is disabled by default.

## Enable from the administrator website

Open **System Settings → Security Settings → Mainland China web access
restriction**, choose the switch state and save. The stored setting takes
precedence over the environment/YAML default, survives restarts and applies
immediately to the process handling the save. Other running instances refresh
their setting cache within five seconds. No container restart is needed to
toggle the restriction after the updated backend/frontend have been installed.
Only administrators can read or update this setting through the existing
protected settings API. It is not part of public frontend configuration.

## Deployment defaults

In the custom source-build deployment's `.env`:

```dotenv
SECURITY_MAINLAND_WEB_BLOCK_ENABLED=true
# Empty means use the bundled IPv4 and IPv6 country CIDR snapshot.
SECURITY_MAINLAND_WEB_BLOCK_CIDR_FILE=
```

The `docker-compose.custom.yml` overlay passes these variables to the app.
Rebuild/install the changed backend and recreate its container. Merely changing
the environment of an older installed build does not add this feature.
For other Compose files add these variables to the application service's
`environment` section, or configure `data/config.yaml`:

```yaml
security:
  mainland_web_block:
    enabled: true
    cidr_file: ""
```

These values are defaults only until the administrator saves a website choice.
To disable an explicitly stored website choice, turn the website switch off
and save. No DNS changes, additional domain or database migration are required.

## Client IP and reverse proxies

This restriction always uses Gin's configured trusted-proxy chain. It ignores
raw country headers and does not use the legacy forwarded-IP compatibility
switch. A directly connected client's forwarding headers cannot change its
classification.

Behind Caddy/Nginx/Docker/CDN, explicitly configure `server.trusted_proxies`
with the actual immediate reverse proxy addresses/subnets, and ensure the
proxy overwrites or safely appends client forwarding headers. See
`EDGE_SECURITY.md`. Do not blindly trust every address or every private subnet.
Without a correct proxy chain the app can see the proxy's IP instead of the
visitor's IP, so the restriction will not work reliably. If a CDN is in front,
correct client-IP restoration is still necessary even though no WAF rule is
needed.

## Country data and caching

Requests do not call an external geolocation API. The bundled IPdeny CN
snapshot includes IPv4 and IPv6; see `backend/internal/pkg/mainlandip/README.md`
for source, date, license and limitations. Classification uses country IP
allocation data, which is an approximation rather than a person's physical
location. Private, loopback and unrecognized addresses are allowed.

To supply newer data without rebuilding, place a complete CN-only CIDR list
with both address families in `data/mainland-cn.zone` and set
`SECURITY_MAINLAND_WEB_BLOCK_CIDR_FILE=/app/data/mainland-cn.zone`. Each line
must be a CIDR or a comment beginning with `#`. Validate the new list, replace
it atomically, and restart the app. Invalid or missing custom data prevents
startup if the effective restriction is enabled; a disabled restriction does
not load or require the optional file. The app never silently uses an empty
block list. A running process retains its loaded snapshot until restart.
Enabling through the website validates the configured data before persisting
the setting. If data becomes unavailable before its first activation, affected
documents return 503 until valid data is available, while other routes continue.

Allowed and denied responses on these three paths use `private, no-store`
cache headers and do not use the SPA's document ETags. Purge previously cached
HTML on those paths when first enabling this feature. A CDN rule that forces
HTML caching despite origin headers must be removed for these paths.

## Verify before declaring deployment complete

Check `/`, `/login` and `/home` from independently verified mainland IPv4 and
IPv6 sources: all should return 403 and the regional notice. Check the same
paths from a non-mainland source: the normal application should load. Check
your actual API routes and the unrelated pages you rely on from both sources.
Do not simulate the country by sending an untrusted `X-Forwarded-For` header
to the public origin; the middleware intentionally ignores such spoofing.

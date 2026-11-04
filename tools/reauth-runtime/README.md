# Excel authorization worker

The application lazily installs the upstream `ranxi2001/sub2api` portable worker
from release `v2.19.4` on Linux amd64/arm64. The version is independent of this
custom application's release number. Both the GitHub asset digest and a pinned
SHA256 in `backend/internal/reauthruntime/runtime.go` must match before execution.
The worker runs with a minimal environment and its stdout/stderr are discarded.
The current backend accepts only local Password/TOTP Excel authorization tasks.

Persist `DATA_DIR` (Docker default `/app/data`), including
`secrets/credential-operations.key` and `credential-worker/`. The administrator can
initialize the dedicated encryption key from the account's Excel authorization
panel; an explicitly configured server encryption key remains supported. A lost
key is never silently replaced while encrypted login data or Excel grants exist.

On macOS/Windows, offline installations, or when using your own worker, configure
the same random `OPENAI_REAUTH_WORKER_TOKEN` (at least 32 characters) in the server
and worker. Setting it on the server selects external mode. Keep this value in a
private environment file, not a command-line argument or source file.

The optional external worker is `tools/openai_oauth_reauth_worker.py`. It requires
Python 3.12, Node 22 and toSub2 pinned at
`8548397e89bf80e508eda64a87e0d556d43abc84`, with the Node/Python dependencies pinned
in this directory. Set `SUB2API_BASE_URL` (HTTPS except localhost), `TOSUB2_ROOT`,
`NODE_EXECUTABLE` when needed, and optionally `OPENAI_REAUTH_CONCURRENCY` (1-16,
default 3). Run `python3 tools/openai_oauth_reauth_worker.py --check` to validate
local configuration without claiming a task, then run it without `--check`.
The saved proxy source can follow the account, select a managed proxy, or acquire
one Mihomo lease for the entire login attempt.

`build.sh` builds an optional portable external worker using Docker. It does not
replace the application's pinned upstream downloads. No real login is performed
by the build tests. The isolated `openai_excel_2fa_login.py` utility is diagnostic
only and writes sensitive token output to its explicitly selected private output
directory; the application uses the authenticated queue protocol instead.

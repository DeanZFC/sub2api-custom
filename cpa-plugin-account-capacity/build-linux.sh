#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CPA_SRC=${CPA_SRC:-/tmp/cliproxyapi-reference}
OUTPUT=${OUTPUT:-"$ROOT_DIR/account-capacity.so"}

if [[ ! -f "$CPA_SRC/go.mod" ]]; then
  mkdir -p "$(dirname "$CPA_SRC")"
  git clone --depth=1 https://github.com/router-for-me/CLIProxyAPI.git "$CPA_SRC"
fi

cd "$ROOT_DIR"
go mod edit "-replace=github.com/router-for-me/CLIProxyAPI/v7=$CPA_SRC"
cleanup() {
  go mod edit -dropreplace github.com/router-for-me/CLIProxyAPI/v7 >/dev/null 2>&1 || true
}
trap cleanup EXIT

CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -buildmode=c-shared -o "$OUTPUT" .
rm -f "${OUTPUT%.so}.h"
file "$OUTPUT"

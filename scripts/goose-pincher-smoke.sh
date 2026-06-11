#!/usr/bin/env bash
set -euo pipefail

# Smoke-test that Goose can see Pincher MCP tools without mutating the user's
# live Pincher database. This intentionally uses a SQLite snapshot because a
# long-running watcher/index pass can hold the live writer lock long enough for
# Goose's stdio extension startup to time out with SQLITE_BUSY.

GOOSE_BIN="${GOOSE_BIN:-goose}"
PINCHER_BIN="${PINCHER_BIN:-pincher}"
WORK_DIR="${WORK_DIR:-$(pwd)}"
PINCHER_LIVE_DATA_DIR="${PINCHER_DATA_DIR:-${HOME}/.local/share/pincherMCP/hermes}"
SNAPSHOT_DIR="${SNAPSHOT_DIR:-$(mktemp -d -t pincher-goose-smoke.XXXXXX)}"
GOOSE_PROVIDER="${GOOSE_PROVIDER:-claude-acp}"
GOOSE_MODEL="${GOOSE_MODEL:-sonnet}"

cleanup() {
  if [[ "${KEEP_SNAPSHOT:-}" != "1" ]]; then
    rm -rf "$SNAPSHOT_DIR"
  fi
}
trap cleanup EXIT

mkdir -p "$SNAPSHOT_DIR"
if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "${PINCHER_LIVE_DATA_DIR}/pincher.db" ".timeout 10000" ".backup '${SNAPSHOT_DIR}/pincher.db'"
else
  cp "${PINCHER_LIVE_DATA_DIR}/pincher.db" "${SNAPSHOT_DIR}/pincher.db"
fi

PINCHER_DATA_DIR="$SNAPSHOT_DIR" "$PINCHER_BIN" health-check >/dev/null

cd "$WORK_DIR"
GOOSE_TIMEOUT_SECONDS="${GOOSE_TIMEOUT_SECONDS:-180}"
timeout "${GOOSE_TIMEOUT_SECONDS}s" "$GOOSE_BIN" run \
  --no-profile \
  --provider "$GOOSE_PROVIDER" \
  --model "$GOOSE_MODEL" \
  --with-extension "PINCHER_DATA_DIR=${SNAPSHOT_DIR} ${PINCHER_BIN}" \
  --no-session \
  --max-turns 4 \
  --text 'Use the Pincher extension once to run a lightweight project list. Then reply in exactly three bullets: tool used, project-list summary, available yes/no.' \
  --output-format text

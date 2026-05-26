#!/usr/bin/env bash
# swap-active-binary_test.sh — contract tests for the install-time swap probe.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$REPO_ROOT/scripts/swap-active-binary.sh"

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

source_bin="$tmp/pincher-source"
target_bin="$tmp/pincher-target"
seen_file="$tmp/seen-data-dir"
live_dir="$tmp/live-default-data"
mkdir -p "$live_dir"

cat >"$source_bin" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  --version)
    echo "pincherMCP test-source"
    ;;
  health-check)
    if [[ -z "${PINCHER_DATA_DIR:-}" ]]; then
      echo "missing PINCHER_DATA_DIR" >&2
      exit 1
    fi
    if [[ "$PINCHER_DATA_DIR" == "$LIVE_DIR" ]]; then
      echo "probe used live data dir" >&2
      exit 1
    fi
    if [[ ! -d "$PINCHER_DATA_DIR" ]]; then
      echo "probe data dir does not exist: $PINCHER_DATA_DIR" >&2
      exit 1
    fi
    printf '%s\n' "$PINCHER_DATA_DIR" >"$SEEN_FILE"
    echo "OK pincher test-source (1 tools)" >&2
    ;;
  *)
    echo "unexpected args: $*" >&2
    exit 2
    ;;
esac
EOF
chmod +x "$source_bin"

cat >"$target_bin" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
  echo "pincherMCP test-target"
  exit 0
fi
exit 0
EOF
chmod +x "$target_bin"

LIVE_DIR="$live_dir" SEEN_FILE="$seen_file" PINCHER_DATA_DIR="$live_dir" \
  bash "$SCRIPT" --source="$source_bin" --target="$target_bin" >/tmp/swap-active-binary-test.out 2>&1

if [[ ! -s "$seen_file" ]]; then
  echo "FAIL: health-check did not record a probe data dir"
  cat /tmp/swap-active-binary-test.out
  exit 1
fi

seen="$(cat "$seen_file")"
if [[ "$seen" == "$live_dir" ]]; then
  echo "FAIL: probe used caller/live PINCHER_DATA_DIR"
  exit 1
fi

if [[ -d "$seen" ]]; then
  echo "FAIL: temporary probe data dir was not cleaned up: $seen"
  exit 1
fi

echo "swap-active-binary_test.sh: all tests passed"

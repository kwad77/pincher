#!/usr/bin/env bash
# scripts/per-tool-latency.sh — FILE-C #1522 v0.91.
#
# Measures per-tool p50 + p99 latency over a fixed query set, compared
# against testdata/bench/per-tool-latency-budget.json. Phase-1 advisory
# in CI (failure logs, does not block); phase-2 required at v1.0 per
# the issue's acceptance criterion.
#
# Methodology:
#   - 100 invocations per tool against the testdata/corpus/go-project
#     pinned corpus.
#   - p50 = sorted[50], p99 = sorted[99].
#   - Compare against the budget JSON; tools NOT in the JSON are exempt.
#
# Each invocation goes through pincher's HTTP gateway (not stdio MCP) so
# we measure the gateway path consumers actually hit. Stdio overhead is
# different and tracked separately.

set -euo pipefail

PINCHER_BIN="${PINCHER_BIN:-$(command -v pincher 2>/dev/null || echo "")}"
if [ -z "${PINCHER_BIN}" ] || [ ! -x "${PINCHER_BIN}" ]; then
  echo "::error::PINCHER_BIN not set and no pincher on PATH" >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "::error::jq not on PATH — per-tool latency gate needs jq for budget parsing" >&2
  exit 2
fi

ITERATIONS="${ITERATIONS:-100}"
BUDGET="$(cd "$(dirname "$0")/.." && pwd)/testdata/bench/per-tool-latency-budget.json"
CORPUS="$(cd "$(dirname "$0")/.." && pwd)/testdata/corpus/go-project"
THRESHOLD_PCT="${THRESHOLD_PCT:-20}"   # +20% over budgeted p99 fails.

if [ ! -f "${BUDGET}" ]; then
  echo "::error::budget JSON missing at ${BUDGET}" >&2
  exit 2
fi
if [ ! -d "${CORPUS}" ]; then
  echo "::error::pinned corpus missing at ${CORPUS}" >&2
  exit 2
fi

work=$(mktemp -d -t pertool-XXXXXX)
trap 'rm -rf "$work"; kill -TERM $SERVER_PID 2>/dev/null || true' EXIT

# Index the pinned corpus once.
"${PINCHER_BIN}" index "${CORPUS}" --data-dir "${work}/data" >/dev/null

# Start HTTP gateway; pick a free loopback-only port and read it from
# the stderr log. Binding bare `:0` is intentionally rejected by the
# default-deny HTTP policy unless auth or an explicit open-bind override
# is configured.
"${PINCHER_BIN}" --data-dir "${work}/data" --no-stdio --http 127.0.0.1:0 >"${work}/srv.out" 2>&1 &
SERVER_PID=$!

# Wait for the bound port. pincher prints `HTTP server listening on
# 127.0.0.1:NNNNN` to stderr on startup.
addr=""
for _ in $(seq 1 30); do
  addr=$(grep -oE '127\.0\.0\.1:[0-9]+' "${work}/srv.out" | head -1 || true)
  [ -n "${addr}" ] && break
  sleep 0.2
done
if [ -z "${addr}" ]; then
  echo "::error::HTTP gateway did not bind within 6s" >&2
  cat "${work}/srv.out" >&2
  exit 1
fi

for _ in $(seq 1 30); do
  if curl -fsSL -m 1 "http://${addr}/v1/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
if ! curl -fsSL -m 1 "http://${addr}/v1/health" >/dev/null 2>&1; then
  echo "::error::HTTP gateway bound ${addr} but did not become ready within 6s" >&2
  cat "${work}/srv.out" >&2
  exit 1
fi

# Tool → invocation body. Bodies are minimal — we want gateway+handler
# latency, not network or query-complexity contributions.
declare -A bodies=(
  [search]='{"query":"main","project":"go-project"}'
  [symbol]='{"id":"cmd/cli/main.go::main.main#Function","project":"go-project"}'
  [context]='{"id":"cmd/cli/main.go::main.main#Function","project":"go-project"}'
  [schema]='{"project":"go-project"}'
  [list]='{}'
  [stats]='{}'
  [health]='{}'
  [architecture]='{"project":"go-project"}'
)

declare -A methods=(
  [search]=POST
  [symbol]=POST
  [context]=POST
  [schema]=POST
  [list]=POST
  [stats]=GET
  [health]=GET
  [architecture]=POST
)

declare -A paths=(
  [search]="/v1/search"
  [symbol]="/v1/symbol"
  [context]="/v1/context"
  [schema]="/v1/schema"
  [list]="/v1/list"
  [stats]="/v1/stats"
  [health]="/v1/health"
  [architecture]="/v1/architecture"
)

results_json="${work}/results.json"
echo "{}" > "${results_json}"

fail_count=0
for tool in "${!bodies[@]}"; do
  budget_p50=$(jq -r --arg t "$tool" '.tools[$t].p50_ms // empty' "${BUDGET}")
  budget_p99=$(jq -r --arg t "$tool" '.tools[$t].p99_ms // empty' "${BUDGET}")
  if [ -z "${budget_p99}" ]; then
    echo "tool ${tool}: not in budget (exempt)"
    continue
  fi

  samples=()
  for _ in $(seq 1 "${ITERATIONS}"); do
    # curl -w outputs the connect/transfer timing in seconds; convert
    # to ms with awk for integer math against the budget.
    if [ "${methods[$tool]}" = "GET" ]; then
      elapsed_s=$(curl -fsSL -m 5 -o /dev/null -w '%{time_total}' \
        "http://${addr}${paths[$tool]}")
    else
      elapsed_s=$(curl -fsSL -m 5 -o /dev/null -w '%{time_total}' \
        -H 'Content-Type: application/json' \
        -d "${bodies[$tool]}" \
        "http://${addr}${paths[$tool]}")
    fi
    elapsed_ms=$(awk -v s="${elapsed_s}" 'BEGIN { printf "%d", s * 1000 }')
    samples+=("${elapsed_ms}")
  done

  # Sort + pick p50/p99. samples has ${ITERATIONS} entries; p50 = index
  # 50, p99 = index 99 (1-indexed at iter=100).
  sorted=$(printf '%s\n' "${samples[@]}" | sort -n)
  p50=$(echo "${sorted}" | sed -n "$((ITERATIONS / 2))p")
  p99=$(echo "${sorted}" | sed -n "$((ITERATIONS - 1))p")

  # Threshold check: p99 must be ≤ budget × (1 + THRESHOLD_PCT/100).
  threshold=$(awk -v b="${budget_p99}" -v pct="${THRESHOLD_PCT}" 'BEGIN { printf "%d", b * (1 + pct / 100) }')
  if [ "${p99}" -gt "${threshold}" ]; then
    echo "::error::tool ${tool}: p99=${p99}ms > threshold=${threshold}ms (budget=${budget_p99}ms +${THRESHOLD_PCT}%)"
    fail_count=$(( fail_count + 1 ))
  else
    echo "tool ${tool}: p50=${p50}ms (budget ${budget_p50}ms), p99=${p99}ms (budget ${budget_p99}ms) — within ±${THRESHOLD_PCT}%"
  fi

  jq --arg t "${tool}" --argjson p50 "${p50}" --argjson p99 "${p99}" \
    '.[$t] = {p50_ms: $p50, p99_ms: $p99}' "${results_json}" > "${results_json}.tmp"
  mv "${results_json}.tmp" "${results_json}"
done

echo
echo "Results JSON:"
cat "${results_json}"

if [ "${fail_count}" -gt 0 ]; then
  echo
  echo "::warning::per-tool-latency: ${fail_count} tool(s) over budget (advisory phase — see #1522 FILE-C)"
  # Phase 1 (v0.91-v0.99): advisory. Exit zero so the workflow stays
  # green; the ::warning:: above surfaces in the run summary. Flip
  # the next line to `exit 1` at v1.0 promotion per FILE-C phase 2.
  exit 0
fi
echo "per-tool-latency: all tools within budget."

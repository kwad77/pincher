#!/usr/bin/env bash
# run-arm.sh — run one loopbench arm as a real `claude -p` session and record usage.
#
# Usage:
#   run-arm.sh <arm.json> <task.md> <outdir> [repo_dir]
#
#   arm.json   arm spec: {name, mcp_config (path|"none"), coaching (path|"none"),
#              disallowed_tools[], allowed_tools[], model? }
#              Relative mcp_config/coaching paths resolve against scripts/loopbench/.
#   task.md    the task prompt, passed verbatim as the -p prompt.
#   outdir     where <arm>.json (raw CLI output), <arm>-answer.md and results.tsv land.
#   repo_dir   cwd for the session (default: the repo root above scripts/loopbench).
#
# Output JSON shape (claude CLI 2.1.x, --output-format json), observed 2026-06-11:
#   .usage.input_tokens / .usage.cache_creation_input_tokens /
#   .usage.cache_read_input_tokens / .usage.output_tokens
#   .total_cost_usd  .num_turns  .duration_ms  .result
set -euo pipefail

usage() { sed -n '2,15p' "$0"; exit 1; }
[ $# -ge 3 ] || usage

ARM_JSON=$1
TASK_MD=$2
OUTDIR=$3
HERE=$(cd "$(dirname "$0")" && pwd)
REPO_DIR=${4:-$(cd "$HERE/../.." && pwd)}

for f in "$ARM_JSON" "$TASK_MD"; do
  [ -f "$f" ] || { echo "run-arm: missing file: $f" >&2; exit 1; }
done
command -v jq >/dev/null || { echo "run-arm: jq is required" >&2; exit 1; }
command -v claude >/dev/null || { echo "run-arm: claude CLI is required" >&2; exit 1; }
mkdir -p "$OUTDIR"

NAME=$(jq -r '.name' "$ARM_JSON")
MCP_CONFIG=$(jq -r '.mcp_config // "none"' "$ARM_JSON")
COACHING=$(jq -r '.coaching // "none"' "$ARM_JSON")
MODEL=$(jq -r '.model // empty' "$ARM_JSON")

# Resolve config paths relative to scripts/loopbench/.
resolve() {
  case $1 in
    none|"") echo "none" ;;
    /*)      echo "$1" ;;
    *)       echo "$HERE/$1" ;;
  esac
}
MCP_CONFIG=$(resolve "$MCP_CONFIG")
COACHING=$(resolve "$COACHING")

CMD=(claude -p --output-format json --strict-mcp-config)
[ -n "$MODEL" ] && CMD+=(--model "$MODEL")
if [ "$MCP_CONFIG" != "none" ]; then
  [ -f "$MCP_CONFIG" ] || { echo "run-arm: mcp_config not found: $MCP_CONFIG" >&2; exit 1; }
  CMD+=(--mcp-config "$MCP_CONFIG")
fi
if [ "$COACHING" != "none" ]; then
  [ -f "$COACHING" ] || { echo "run-arm: coaching file not found: $COACHING" >&2; exit 1; }
  CMD+=(--append-system-prompt-file "$COACHING")
fi
# Tool lists: comma-joined into a single argument — the CLI's variadic
# `--allowedTools <tools...>` would otherwise swallow following arguments.
DISALLOWED=$(jq -r '.disallowed_tools // [] | join(",")' "$ARM_JSON")
ALLOWED=$(jq -r '.allowed_tools // [] | join(",")' "$ARM_JSON")
[ -n "$DISALLOWED" ] && CMD+=(--disallowedTools "$DISALLOWED")
[ -n "$ALLOWED" ] && CMD+=(--allowedTools "$ALLOWED")

RAW="$OUTDIR/$NAME.json"
ANSWER="$OUTDIR/$NAME-answer.md"
TSV="$OUTDIR/results.tsv"
TASK_BASE=$(basename "$TASK_MD")
TASK_TEXT=$(cat "$TASK_MD")   # read before the cd below — TASK_MD may be relative

echo "run-arm: arm=$NAME task=$TASK_BASE repo=$REPO_DIR" >&2
echo "run-arm: ${CMD[*]} < $TASK_BASE" >&2

# Prompt goes in via stdin so no variadic flag can swallow it.
set +e
(cd "$REPO_DIR" && "${CMD[@]}" <<<"$TASK_TEXT") \
  >"$RAW" 2>"$OUTDIR/$NAME.stderr.log"
RC=$?
set -e
if [ $RC -ne 0 ] || ! jq -e .usage "$RAW" >/dev/null 2>&1; then
  echo "run-arm: arm $NAME failed (rc=$RC); see $RAW and $OUTDIR/$NAME.stderr.log" >&2
  exit 1
fi

jq -r '.result' "$RAW" >"$ANSWER"

if [ ! -f "$TSV" ]; then
  printf 'arm\ttask\tmodel\tnum_turns\tinput_tokens\tcache_creation\tcache_read\toutput_tokens\ttotal_tokens\tcost_usd\tduration_ms\tis_error\n' >"$TSV"
fi
jq -r --arg arm "$NAME" --arg task "$TASK_BASE" --arg model "${MODEL:-default}" '
  [ $arm, $task, $model,
    (.num_turns // 0),
    (.usage.input_tokens // 0),
    (.usage.cache_creation_input_tokens // 0),
    (.usage.cache_read_input_tokens // 0),
    (.usage.output_tokens // 0),
    ((.usage.input_tokens // 0) + (.usage.cache_creation_input_tokens // 0)
      + (.usage.cache_read_input_tokens // 0) + (.usage.output_tokens // 0)),
    (.total_cost_usd // 0),
    (.duration_ms // 0),
    (.is_error // false)
  ] | @tsv' "$RAW" >>"$TSV"

echo "run-arm: done — $(tail -1 "$TSV")" >&2

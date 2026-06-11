#!/usr/bin/env bash
# score.sh — render a markdown scoreboard from a loopbench results.tsv.
#
# Usage: score.sh <outdir>   (expects <outdir>/results.tsv written by run-arm.sh)
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: score.sh <outdir>" >&2; exit 1; }
TSV="$1/results.tsv"
[ -f "$TSV" ] || { echo "score: no results.tsv in $1" >&2; exit 1; }

echo "# loopbench scoreboard"
echo
echo "Source: \`$TSV\` ($(($(wc -l <"$TSV") - 1)) run(s))"
echo
echo "| arm | task | model | turns | total tokens | input | cache-create | cache-read | output | cost (USD) | wall (s) | error |"
echo "|---|---|---|--:|--:|--:|--:|--:|--:|--:|--:|---|"
# Sort by total tokens ascending (column 9), skipping the header.
tail -n +2 "$TSV" | sort -t$'\t' -k9,9n | awk -F'\t' '{
  printf "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %.4f | %.1f | %s |\n",
    $1, $2, $3, $4, $9, $5, $6, $7, $8, $10, $11/1000, $12
}'
echo
echo "_total tokens = input + cache_creation + cache_read + output (all billed categories)._"

# Architecture report token-savings comparison

This artifact reconciles the remaining token-savings acceptance evidence for #1912 using the current Pincher self-index. It is a deterministic measurement note, not a pricing or universal-savings claim.

## Scope

- Project: `/home/kwad77/pincher`
- Report command: `./pincher report --project /home/kwad77/pincher --format markdown --out PINCHER_REPORT.md`
- Measurement date: 2026-06-09
- Token estimate: `floor(bytes / 4)`, matching Pincher's documented response-size token heuristic.
- Source data: the generated report uses Pincher's existing symbol and edge index; the comparison baselines use local tracked-file bytes and path lists to model common raw exploration alternatives.

## Result

| Artifact / baseline | Bytes | Estimated tokens | Notes |
|---|---:|---:|---|
| `pincher report` markdown | 11,485 | 2,871 | Compact report generated from indexed graph evidence. |
| Raw `README.md` only | 15,021 | 3,755 | Narrative onboarding baseline; does not include graph edges, hotspots, or provenance sections. |
| Raw tracked file tree (`git ls-files`) | 38,816 | 9,704 | Path-list orientation baseline. |
| Raw `README.md` + tracked file tree | 53,837 | 13,459 | Minimal raw orientation bundle before reading source files. |
| Raw tracked full-file corpus | 9,190,009 | 2,297,502 | All currently tracked file bytes. |

Derived comparisons:

- Report vs `README.md` + file tree: 78.67% fewer estimated tokens.
- Report vs tracked full-file corpus: 99.88% fewer estimated tokens.

These percentages are falsifiable outputs of the byte counts above. They are not dollar savings and they do not claim the report replaces targeted follow-up calls; the report is intended to choose better next Pincher calls before raw file exploration.

## Reproduction

From the repository root:

```bash
set -euo pipefail
TMP=$(mktemp -d)
./pincher report --project /home/kwad77/pincher --format markdown --out "$TMP/PINCHER_REPORT.md"
python3 - <<'PY' "$TMP/PINCHER_REPORT.md" /home/kwad77/pincher
import os, sys, subprocess, json
report_path=sys.argv[1]
root=sys.argv[2]
def tok_bytes(n): return max(1, n//4)
def size(path): return os.path.getsize(path)
report_b=size(report_path)
readme_b=size(os.path.join(root,'README.md'))
files=subprocess.check_output(['git','ls-files'], cwd=root, text=True).splitlines()
tree_b=len(('\n'.join(files)+'\n').encode())
full_b=sum(os.path.getsize(os.path.join(root,f)) for f in files)
print(json.dumps({
  'report_bytes': report_b,
  'report_tokens_est': tok_bytes(report_b),
  'readme_bytes': readme_b,
  'readme_tokens_est': tok_bytes(readme_b),
  'tree_files': len(files),
  'tree_bytes': tree_b,
  'tree_tokens_est': tok_bytes(tree_b),
  'readme_plus_tree_tokens_est': tok_bytes(readme_b + tree_b),
  'full_file_bytes': full_b,
  'full_file_tokens_est': tok_bytes(full_b),
  'report_vs_readme_plus_tree_savings_pct': round((1 - tok_bytes(report_b)/tok_bytes(readme_b + tree_b))*100, 2),
  'report_vs_full_file_savings_pct': round((1 - tok_bytes(report_b)/tok_bytes(full_b))*100, 2),
}, indent=2))
PY
```

## Non-goals and missing-data policy

- No LLM extraction was used.
- No inferred architecture prose is added by this artifact.
- No unsupported cost or ROI estimate is made.
- If a future run cannot read the report, README, tree, or tracked files, the measurement should report the missing input rather than filling in an estimate.

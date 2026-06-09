# Rationale precision audit — Pincher self-index

Date: 2026-06-09
Project: `/home/kwad77/pincher`
Source: Pincher `search` / `query` over the live self-index for rationale-tagged comments.
Scope: v1.2 #1913 rationale-map acceptance evidence.
Evidence boundary: the live self-index currently reports 53 `Rationale` symbols; this audit samples 50 rows and does not claim full-population semantic usefulness.

## Summary

This audit samples 50 rationale nodes from Pincher's own indexed graph. The audit checks whether each row is extracted evidence from a tagged comment (`WHY`, `NOTE`, `HACK`, `TODO`, `BUG`, etc.) rather than inferred commentary.

Results:

- Sample size: 50 rationale nodes.
- Extraction precision for "tagged rationale/commentary node": 50/50.
- High-value design-intent rows: 47/50.
- Weak-but-correct rows: 3/50. These are still extracted tagged comments, but their prose is terse or taxonomy-like rather than strong design rationale.
- Inferred rows: 0/50.
- Ambiguous/missing attachment handling: tracked by report output as `missing_or_ambiguous_attachment` / `attachment_state`; this audit does not infer missing parents.

Important boundary: this audit validates the deterministic extractor/report surface. It does not claim that every tagged comment is equally useful design intent, only that the surface distinguishes extracted comments from inference and gives maintainers enough file/span/provenance context to judge usefulness.

## Method

1. Query the self-index for rationale-like tags and validate the rationale population with Pincher graph query (`MATCH (n:Rationale) ...` returned 53 rows at audit time).
2. Keep only rows whose indexed kind is `Rationale`.
3. Sample 50 rows from Pincher's returned rationale rows.
4. Mark each row as:
   - `true_positive`: extracted tagged rationale/commentary evidence.
   - `weak_true_positive`: extracted tagged evidence, but weak design-intent value.
   - `false_positive`: not a tagged rationale/commentary row.
5. Do not infer intent beyond the indexed snippet.

## Sampled rows

| # | Verdict | File:span | Tag/name | Confidence | Notes |
|---:|---|---|---|---:|---|
| 1 | true_positive | `internal/ast/extractor.go:777-782` | NOTE: /HACK/WHY/FIXME/XXX/TODO/BUG annotations that carry the reasoning | 1.00 | Extractor design note; strong rationale. |
| 2 | true_positive | `internal/ast/go_rationale_test.go:77` | HACK: is inside process(). | 1.00 | Test fixture proves parent attachment. |
| 3 | true_positive | `internal/init/vscode_mcp.go:19-48` | WHY: a separate target from `vscode`: | 1.00 | Explicit design distinction. |
| 4 | weak_true_positive | `internal/index/binary_version_stamp_test.go:49` | NOTE: SetBinaryVersion not called. | 1.00 | Correct tagged note; terse. |
| 5 | weak_true_positive | `internal/server/ready_probe_test.go:64` | NOTE: no Authorization header. | 1.00 | Correct tagged note; terse fixture state. |
| 6 | true_positive | `cmd/pinch/snapshot_test.go:40-44` | WHY: this test exists alongside `make corpus-test`: | 1.00 | Strong test rationale. |
| 7 | true_positive | `internal/ast/go_rationale_test.go:84` | TODO: is inside the *Widget.Render method. | 1.00 | Test fixture proves method attachment. |
| 8 | true_positive | `internal/server/server.go:616-635` | WHY: this matters: the binary's _meta.capabilities tag | 1.00 | Strong runtime/schema rationale. |
| 9 | weak_true_positive | `internal/server/server.go:11389` | BUG: fix, error, broken behaviour | 1.00 | Correct tagged row; taxonomy-like/weak semantics. |
| 10 | true_positive | `internal/db/db.go:59-71` | WHY: two pools (#51): SQLite WAL allows concurrent readers without | 1.00 | Strong storage/concurrency rationale. |
| 11 | true_positive | `internal/server/context_for_task.go:15-31` | WHY: this exists (positioning frame): pincher's atomic tools (search / | 1.00 | Strong product/workflow rationale. |
| 12 | true_positive | `internal/server/neighborhood.go:19-29` | WHY: this exists: in-file refactor planning frequently touches multiple | 1.00 | Strong tool-design rationale. |
| 13 | true_positive | `internal/server/notify_log_test.go:192-196` | WHY: this case matters: notifyLog is called from a background | 1.00 | Strong reliability rationale. |
| 14 | true_positive | `internal/ast/python_daemon_bench_1685_test.go:16-20` | WHY: a ratio assertion, not an absolute: subprocess-spawn timing is | 1.00 | Strong CI-flake rationale. |
| 15 | true_positive | `internal/ast/extractor.go:2543-2561` | WHY: the regex extractors are scope-blind. A file with multiple | 1.00 | Strong extraction/identity rationale. |
| 16 | true_positive | `internal/ast/hcl.go:479-483` | WHY: source-order positional suffix and not a label-derived one: | 1.00 | Strong schema/identifier rationale. |
| 17 | true_positive | `internal/db/db_test.go:758-762` | WHY: this gate matters: without it, a new method authored under | 1.00 | Strong regression-gate rationale. |
| 18 | true_positive | `cmd/pinch/doctor.go:1077-1099` | WHY: this matters: pincher's Read/Grep → search redirect (#627) is | 1.00 | Strong observability/adoption rationale. |
| 19 | true_positive | `cmd/pinch/hook_check_prose_exemption_1656_test.go:15-19` | WHY: hook visibly fires on every Read/Grep regardless of whether | 1.00 | Strong false-positive rationale. |
| 20 | true_positive | `internal/server/notify_log_test.go:228-231` | WHY: this matters: if the order ever flips — exit before notify — | 1.00 | Strong ordering/reliability rationale. |
| 21 | true_positive | `internal/server/metrics.go:34-55` | WHY: not prometheus/client_golang: the official library is the gold | 1.00 | Strong dependency/product rationale. |
| 22 | true_positive | `internal/ast/go_rationale_test.go:70` | NOTE: sits at file scope — no enclosing func. | 1.00 | Test fixture for missing attachment. |
| 23 | true_positive | `internal/server/guide_audit_natural_words_test.go:45-48` | NOTE: "find functions with no callers" routes to | 1.00 | Correct routing test rationale. |
| 24 | true_positive | `internal/server/context_for_task.go:527-529` | NOTE: jsonResultWithMeta stamps the standard envelope. The composite | 1.00 | Correct envelope/routing rationale. |
| 25 | true_positive | `internal/ast/sql.go:30-50` | WHY: regex over AST: no pure-Go multi-dialect SQL parser exists. | 1.00 | Strong parser strategy rationale. |
| 26 | true_positive | `internal/ast/extractor.go:3350-3354` | WHY: post-process rather than fix the funcRE: the regex pipeline assumes | 1.00 | Strong implementation strategy rationale. |
| 27 | true_positive | `internal/db/db.go:3528-3544` | WHY: this matters: CPU profiling of the cold/force Index() path showed | 1.00 | Strong performance rationale with numbers. |
| 28 | true_positive | `internal/db/db.go:3614-3619` | WHY: both: the global symbol-ID format collides on identically-laid-out | 1.00 | Strong project-scoping/security rationale. |
| 29 | true_positive | `cmd/pinch/selftest.go:34-38` | WHY: a separate subcommand vs. just `go test`: this verifies the SHIPPED | 1.00 | Strong shipped-binary rationale. |
| 30 | true_positive | `internal/server/server.go:1561-1579` | WHY: the supervisor's auto-restart-on-drift respawns the inner onto | 1.00 | Strong stale-index healing rationale. |
| 31 | true_positive | `internal/server/server.go:1745-1758` | WHY: this matters even after PR #44: pre-fix, a request with | 1.00 | Strong auth edge-case rationale. |
| 32 | true_positive | `internal/server/next_steps_adherence_test.go:237-240` | NOTE: makeReq leaves req.Params.Name unset; CheckAndConsume runs | 1.00 | Correct instrumentation test rationale. |
| 33 | true_positive | `internal/ast/yaml.go:245-248` | WHY: path-based: YAML doesn't carry schema; a generic Helm or k8s | 1.00 | Strong schema-absence rationale. |
| 34 | true_positive | `internal/db/path.go:85-87` | WHY: ASCII-only: case folding for non-ASCII (e.g. Turkish dotted-I, | 1.00 | Strong portability rationale. |
| 35 | true_positive | `cmd/pinch/bench_test.go:20-22` | WHY: on-disk files: the bench's baseline calculation is os.Stat-based | 1.00 | Strong benchmark validity rationale. |
| 36 | true_positive | `internal/cypher/arithmetic_hint_test.go:23-27` | NOTE: `*` and `/` hit special tokenizer paths in pinchQL (HOPS for | 1.00 | Correct tokenizer test rationale. |
| 37 | true_positive | `internal/server/server.go:11638-11642` | NOTE: "split", "move" intentionally NOT in this list — both are | 1.00 | Strong false-positive rationale. |
| 38 | true_positive | `internal/server/drift.go:124-128` | NOTE: this is the COMPLEMENT direction of driftFor — driftFor warns | 1.00 | Strong drift semantics rationale. |
| 39 | true_positive | `internal/server/server_test.go:4169` | NOTE: no SetBasePath — the prefix comes only from the header. | 1.00 | Correct test setup rationale. |
| 40 | true_positive | `internal/server/openapi_output_schemas.go:579-580` | BUG: -hunt composite: parses stack trace, ranks suspects, unions | 1.00 | Correct composite-tool rationale. |
| 41 | true_positive | `internal/ast/ts_nested_function_var_scope_test.go:186-189` | NOTE: JS uses the AST extractor (#266), so this serves as | 1.00 | Correct cross-extractor test rationale. |
| 42 | true_positive | `internal/ast/markdown_test.go:246-251` | NOTE: real Cursor rule files often have YAML frontmatter delimited by | 1.00 | Strong Markdown parser rationale. |
| 43 | true_positive | `cmd/pinch/supervised.go:46-49` | NOTE: passes through any args after `supervised` to the inner pincher | 1.00 | Correct supervised-mode rationale. |
| 44 | true_positive | `sdks/go/examples/search.go:6-16` | NOTE: the build tag above excludes this file from the default | 1.00 | Strong SDK/build rationale. |
| 45 | true_positive | `internal/index/indexer.go:2714-2717` | BUG: PR left historical rows in extraction_failures forever — the | 1.00 | Correct bug-history rationale. |
| 46 | true_positive | `internal/server/fetch_header_prefix_test.go:40-44` | NOTE: <header> is NOT in the strip-list (only <head>, the document | 1.00 | Strong parser bug rationale. |
| 47 | true_positive | `internal/server/server.go:6461-6465` | NOTE: searchTotal is a LOWER BOUND on the true match count for | 1.00 | Strong API semantics rationale. |
| 48 | true_positive | `internal/index/extraction_failures_gc_test.go:14-22` | BUG: PRs left historical rows in the table forever — user repro: | 1.00 | Correct regression rationale. |
| 49 | true_positive | `internal/server/server.go:4497-4504` | BUG: -hunt composite: takes an error_text (stack trace, panic dump, | 1.00 | Correct composite-tool rationale. |
| 50 | true_positive | `internal/ast/ast_test.go:784-787` | NOTE: bare-prefix macros (e.g. `EXPORT_SYMBOL(foo);` at column 0) are | 1.00 | Strong limitation rationale. |

## Observations

- The extractor is precise for tagged-comment extraction in this sample: no rows were invented, summarized, or inferred.
- The weakest rows are still useful as fixture/provenance markers, but downstream reports should keep `source=extracted` and `inferred=false` visible so users can judge semantic value.
- Attachment ambiguity should remain explicit. File-scope/ambiguous comments are not failures; they are useful as long as the report counts them separately and labels `attachment_state=missing_or_ambiguous`.
- The existing report surface now consumes the same rationale-map helper instead of re-implementing grouping inline.

## Follow-up candidates

- Add a first-class MCP/HTTP rationale-map endpoint only if report JSON proves insufficient for downstream consumers.
- Expand extraction method labels if non-Go rationale extraction is added later; do not reuse `go_comment_tag` for other extractors.

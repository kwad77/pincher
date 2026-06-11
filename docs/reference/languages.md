# Language support

[Back to reference index](README.md)

| Language | Extraction | Confidence | Symbol kinds extracted |
|---|---|---|---|
| Go | `go/ast` full AST | 1.0 | Functions, Methods, Types, Interfaces, Structs, Constants, Variables |
| YAML / JSON | `gopkg.in/yaml.v3` Node tree | 1.0 | Settings (dotted-path keys, sequence elements, multi-doc-aware). Ansible-aware `RENDERS` edges for `template: src:`. |
| Bash | `mvdan.cc/sh/v3/syntax` (the `shfmt` parser) | 1.0 | Functions (POSIX `name() { … }` and reserved-word `function name { … }`; `.sh`, `.bash`) |
| HCL / Terraform | `github.com/hashicorp/hcl/v2/hclsyntax` | 1.0 | Resources, DataSources, Modules, Variables, Outputs, Locals, Providers, plus `Block` for nested `lifecycle` / `provisioner` / `connection` / `dynamic` / `backend` / `required_providers`. `.tfvars` assignments emit `Setting`. `var.NAME` references emit `REFERENCES` edges. Covers `.tf`, `.tfvars`. |
| TOML | `github.com/BurntSushi/toml` parseability gate + structural source-walk | 1.0 | One `Setting` per section header and per key assignment with dotted qualified names. Array-of-tables indexed as `name.0`, `name.1`. Multi-line strings/arrays span their full body. Covers `.toml`. |
| Markdown | `github.com/yuin/goldmark` CommonMark | 1.0 | One `Section` symbol per heading. Hierarchical dotted-path qualified name (`intro.getting_started.installation`). Each Section's byte range covers its full body. Covers `.md`, `.markdown`, `.mdx`, `.mdc`. |
| Jinja2 | `github.com/nikolalohinski/gonja` parser | 1.0 | `{% macro %}` → Function, `{% block %}` → Block, `{% set %}` → Setting, `{% extends/include/import/from %}` → IMPORTS edges. 2-second per-file parse timeout protects against gonja lexer hangs on truncated input. Covers `.j2`, `.jinja`, `.jinja2`. |
| Python | Regex → CPython AST (dispatcher upgrades when `python` is on PATH) | 0.85 → 1.0 | Functions, Classes, Methods |
| TypeScript / TSX | Regex | 0.85 | Functions, Classes, Interfaces, Methods |
| JavaScript / JSX | Regex → AST (dispatcher, default-on since v0.20) | 0.85 → 1.0 | Functions, Classes, Methods |
| Rust | Regex → tree-sitter AST (dispatcher, ADR-0008) | 0.85 → 1.0 | Functions, Structs, Traits, Impls, Enums |
| Java | Regex | 0.85 | Classes, Methods, Interfaces |
| Makefile | Regex | 0.85 | Rule targets → Function (`.PHONY` → `IsExported=true`), variable assignments → Setting. Detected by basename (`Makefile`, `GNUmakefile`, lowercase `makefile`) + extension (`.mk`, `.mak`). |
| SQL | Regex | 0.85 | `CREATE TABLE`/`VIEW` → Class; `CREATE FUNCTION`/`PROCEDURE`/`TRIGGER` → Function (handles `IF NOT EXISTS`). Schema prefix split into `qualified_name` (`auth.users`) with bare `name` (`users`). Dialect-aware quoting (backticks/quotes/brackets). Comment-aware. Covers `.sql`, `.ddl`. |
| Ruby | Regex | 0.70 | Functions, Classes, Methods |
| PHP | Regex | 0.85 | Functions, Classes, Methods |
| C / C++ | Regex | 0.85 | Functions, Structs, Classes |
| C# | Regex | 0.85 | Classes, Methods, Interfaces |
| Kotlin | Regex | 0.85 | Functions, Classes |
| Swift | Regex | 0.85 | Functions, Classes |

YAML/JSON files emit one `Setting` symbol per key with a dotted-path qualified name (e.g. `services.web.image`, `tasks.0.name`). Multi-document YAML uses a `docN` prefix. Each Setting's byte range covers the key plus its full nested value, so retrieving `services.web` returns the entire `web` block.

### Capability matrix (#1253)

The 9-axis honest breakdown. `✅` = supported, `⚠️` = partial / language-tier limitation, `❌` = not yet. Source-of-truth columns (Symbols, Same-file calls, Cross-file calls, Tier) are derived from `internal/ast/registry.go` and the resolver gates in `internal/index/indexer.go`. Anyone shipping a new extractor adds a row here in the same PR.

| Language | Detection | Symbols | Imports | Same-file calls | Cross-file calls | Type / receiver | Docstrings | Test files | Tier |
|---|---|---|---|---|---|---|---|---|---|
| Go | `.go` | ✅ Function/Method/Type/Interface/Struct/Const/Var | ✅ | ✅ | ✅ (resolver) | ✅ (v0.57 [#760](https://github.com/kwad77/pincher/issues/760)) | ✅ | ✅ `*_test.go` | AST 1.0 |
| Python | `.py` | ✅ Function/Class/Method | ✅ | ✅ | ✅ (v0.57 [#856](https://github.com/kwad77/pincher/issues/856)) | ❌ | ⚠️ partial | ✅ `test_*.py` / `*_test.py` | AST 1.0 |
| YAML / JSON | `.yaml/.yml/.json` | ✅ Setting (dotted-path) | ⚠️ `RENDERS` (Ansible templates) | n/a | n/a | n/a | n/a | n/a | AST 1.0 |
| Bash | `.sh/.bash` | ✅ Function | ❌ | ❌ | ❌ | n/a | ❌ | ✅ `_test.sh` / `test_*.sh` ([#1213](https://github.com/kwad77/pincher/issues/1213)) | AST 1.0 |
| HCL / Terraform | `.tf/.tfvars` | ✅ Resource/DataSource/Module/Variable/Output/Local/Provider/Block | ⚠️ `REFERENCES` (`var.X`) | n/a | n/a | n/a | n/a | n/a | AST 1.0 |
| TOML | `.toml` | ✅ Setting (per section / per key) | n/a | n/a | n/a | n/a | n/a | n/a | AST 1.0 |
| Markdown | `.md/.markdown/.mdx/.mdc` | ✅ Section (heading hierarchy) | n/a | n/a | n/a | n/a | n/a | n/a | AST 1.0 |
| Jinja2 | `.j2/.jinja/.jinja2` | ✅ Function (macro) / Block / Setting | ✅ `extends/include/import/from` | n/a | n/a | n/a | n/a | n/a | AST 1.0 |
| TypeScript / TSX | `.ts/.tsx` | ✅ Function/Class/Interface/Method | ✅ | ✅ ([#1158](https://github.com/kwad77/pincher/pull/1158)) | ❌ (tracked: [#1177](https://github.com/kwad77/pincher/issues/1177)) | ❌ | ❌ | ✅ `*.test.ts/*.spec.ts` | Regex 0.85 |
| JavaScript / JSX | `.js/.jsx/.mjs/.cjs` | ✅ Function/Class/Method | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ `*.test.js/*.spec.js` | AST 1.0 (dispatcher) |
| Rust | `.rs` | ✅ Function/Struct/Trait/Impl | ⚠️ partial | ✅ (v0.62 [#1159](https://github.com/kwad77/pincher/pull/1159)) | ❌ (tracked: [#1182](https://github.com/kwad77/pincher/issues/1182)) | ❌ | ❌ | ⚠️ `#[cfg(test)]` blocks | AST 1.0 (tree-sitter dispatcher) |
| Java | `.java` | ✅ Class/Method/Interface | ⚠️ partial | ✅ (v0.62) | ❌ (tracked: [#1183](https://github.com/kwad77/pincher/issues/1183)) | ❌ | ⚠️ Javadoc partial | ✅ `*Test.java` | Regex 0.85 |
| Makefile | `Makefile/.mk` | ✅ Function (rule target) / Setting | ❌ | ❌ | ❌ | n/a | ❌ | ❌ | Regex 0.85 |
| SQL | `.sql/.ddl` | ✅ Function/Class (table/view) | ❌ | ❌ | ❌ | n/a | ❌ | ❌ | Regex 0.85 |
| Ruby | `.rb` | ✅ Function/Class/Method | ❌ | ✅ (v0.62) | ❌ | ❌ | ❌ | ⚠️ partial | Regex 0.70 |
| PHP | `.php` | ✅ Function/Class/Method | ❌ | ✅ (v0.62) | ❌ | ❌ | ❌ | ❌ | Regex 0.85 |
| C / C++ | `.c/.h/.cpp/.hpp/.cc` | ✅ Function/Struct/Class | ❌ | ✅ (v0.62) | ❌ | ❌ | ❌ | ❌ | Regex 0.85 |
| C# | `.cs` | ✅ Class/Method/Interface | ❌ | ✅ (v0.62) | ❌ | ❌ | ❌ | ❌ | Regex 0.85 |
| Kotlin | `.kt/.kts` | ✅ Function/Class | ❌ | ✅ (v0.62) | ❌ | ❌ | ❌ | ❌ | Regex 0.85 |
| Swift | `.swift` | ✅ Function/Class | ❌ | ✅ (v0.62) | ❌ | ❌ | ❌ | ❌ | Regex 0.85 |
| Scala | `.scala/.sc` | ✅ Function/Class (v0.63 [#1187](https://github.com/kwad77/pincher/pull/1187)) | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | Regex 0.70 |
| Lua | `.lua` | ✅ Function (v0.63 [#1186](https://github.com/kwad77/pincher/pull/1186)) | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | Regex 0.70 |
| Zig | `.zig` | ✅ Function/Struct (v0.63) | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | Regex 0.70 |
| Elixir | `.ex/.exs` | ✅ Function/Module (v0.63) | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | Regex 0.70 |
| Dart | `.dart` | ✅ Function/Class (v0.63) | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | Regex 0.70 |
| R | `.r/.R` | ✅ Function (v0.63) | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | Regex 0.70 |
| Haskell | `.hs/.lhs` | ❌ (no extractor — [#1161](https://github.com/kwad77/pincher/issues/1161)) | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | Stub 0.0 |

**Reading the matrix:** the gradient runs top-to-bottom — AST-tier languages get full edge graphs and resolver coverage, regex-tier emits symbols + same-file edges only, stub-tier (Haskell only as of v0.63) returns zero symbols.

**Cross-file calls** is where most of the v0.65+ resolver work is concentrated: TypeScript ([#1177](https://github.com/kwad77/pincher/issues/1177)), Rust ([#1182](https://github.com/kwad77/pincher/issues/1182)), Java ([#1183](https://github.com/kwad77/pincher/issues/1183)) are the next AST/resolver work to flip those cells from ❌ to ✅.

**Type / receiver resolution** is the highest-leverage missing axis on regex-tier languages — without it, `X.method()` can't bind to a specific receiver type's method definition, so `trace name=method` returns every same-named method across the project. Tracked alongside the AST roadmap.

### v1.0 fitness declaration (FILE-N · [#1533](https://github.com/kwad77/pincher/issues/1533))

For v1.0, each language gets one of four explicit support statuses. Pincher will provide tests, fix bugs, and accept feature work according to the tier; this declaration says exactly what users can plan against.

| Status | What pincher promises |
|---|---|
| `promised` | Production-grade. AST/parser extraction at 1.0 confidence. Every tool works against this language. Regressions block release. |
| `supported` | Good enough for daily use. Stable-regex extraction at 0.85 confidence. Symbols + same-file edges are reliable; cross-file edges and type-resolution are tracked but not gated. |
| `best-effort` | Useful, with known gaps. Approximate-regex extraction at 0.70 confidence. Symbols are usable; edge graph is limited. We fix reported bugs but won't gate release on them. |
| `excluded` | Not supported in v1.0. The language has an entry in the tier list but the extractor is stub-quality. Tracked for a post-v1.0 release. |

| Language | Tier | v1.0 status | Known limitations |
|---|---|---|---|
| Go | AST 1.0 | **promised** | None — reference implementation |
| Python | AST 1.0 (dispatcher) | **promised** | Docstring extraction partial; AST upgrade requires `python` on PATH at index time |
| YAML / JSON | AST 1.0 | **promised** | Sequence-rename ID instability (#205, won't-fix for 1.0) — workaround: search by name not id |
| Bash | AST 1.0 | **promised** | No cross-file CALLS (Bash sourcing model differs from import) |
| HCL / Terraform | AST 1.0 | **promised** | `REFERENCES` edges only via `var.X` — no full HCL expression resolution |
| TOML | AST 1.0 | **promised** | None significant — pure structural extraction |
| Markdown | AST 1.0 | **promised** | None — heading hierarchy is the whole contract |
| Jinja2 | AST 1.0 | **promised** | 2-second parse timeout on truncated input (gonja lexer hang guard) |
| JavaScript / JSX | AST 1.0 (dispatcher since #1328 v0.71) | **promised** | Type resolution absent; cross-file calls per #1177 |
| TypeScript / TSX | Regex 0.85 | **supported** | Cross-file calls absent (#1177); type/receiver resolution absent |
| Rust | AST 1.0 (tree-sitter, ADR-0008) | **promised** | Real tree-sitter via WASM (clean parse → 1.0, else regex fallback). Cross-file calls absent (#1182); receiver type resolution absent |
| Java | Regex 0.85 | **supported** | Cross-file calls absent (#1183 v0.87); Javadoc partial |
| C# | Regex 0.85 | **supported** | Cross-file calls absent; receiver type resolution absent |
| PHP | Regex 0.85 | **supported** | Cross-file calls absent; namespaces partial |
| Kotlin | Regex 0.85 | **supported** | Cross-file calls absent |
| Swift | Regex 0.85 | **supported** | Cross-file calls absent. AST upgrade tracked (#1452 v0.85) but the swift-syntax subprocess pattern is optional — until it ships, regex tier is the v1.0 promise |
| C | Regex 0.85 | **supported** | Cross-file calls absent; macros not expanded |
| C++ | Regex 0.85 | **supported** | Same as C, plus template instantiation not tracked |
| Makefile | Regex 0.85 | **supported** | Includes not resolved cross-file |
| SQL | Regex 0.85 | **supported** | No edge graph between table/view/function entities |
| Ruby | Regex 0.70 | **best-effort** | Cross-file calls absent; metaprogramming patterns produce gaps |
| Scala | Regex 0.70 | **best-effort** | Cross-file calls absent; implicit conversions invisible |
| Lua | Regex 0.70 | **best-effort** | Cross-file calls absent; dynamic-dispatch patterns invisible |
| Zig | Regex 0.70 | **best-effort** | Cross-file calls absent; comptime invisible |
| Elixir | Regex 0.70 | **best-effort** | Cross-file calls absent; macro-defined functions partial |
| Dart | Regex 0.70 | **best-effort** | Cross-file calls absent |
| R | Regex 0.70 | **best-effort** | Cross-file calls absent; S3/S4 dispatch invisible |
| Haskell | Stub 0.0 | **excluded** | Indentation-sensitive layout requires hardened regex or full parser — tracked post-v1.0 (#1161) |

**Reading this table.**

- `promised` languages get every regression treated as a 1.x patch-line bug. If something breaks for a `promised` language at v1.0+, the next patch ships a fix.
- `supported` languages get bug fixes but cross-file resolver gaps are accepted as known limitations. Working around them with `neighborhood` (same-file siblings) instead of `trace direction=out` (cross-file) is the recommended path until the AST upgrade lands.
- `best-effort` languages produce useful symbol search results. The edge graph is sparse; treat `trace` and `query` over edges as best-effort. The regex extractor's false-positive rate is the visible cost.
- `excluded` languages return zero symbols. The extractor's stub status is intentional and load-bearing — index time stays bounded regardless of how many Haskell files end up in a project.

**v1.0 surface promise.** Per [ADR-0002](../adr/0002-v1-frozen-surface.md), the *tier* assignments and *v1.0 status* values above are part of the frozen v1.0 surface. Promotion (e.g. Rust regex-0.85 → AST-1.0) is a minor-release additive feature in 1.x. Demotion (a language's status going DOWN) is a breaking change and requires a 2.0.

### Skip rules

The indexer refuses to extract from files that are guaranteed to produce noise rather than signal, regardless of extension:

- **Lockfiles** by exact basename: `package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`, `bun.lock(b)`, `Cargo.lock`, `composer.lock`, `Gemfile.lock`, `Pipfile.lock`, `poetry.lock`, `uv.lock`, `pdm.lock`, `mix.lock`, `pubspec.lock`, `Podfile.lock`, `Cartfile.resolved`, `Package.resolved`, `flake.lock`, `go.sum`. Without this rule a 700 KB `package-lock.json` would emit thousands of low-signal `Setting` symbols.
- **Minified bundles** by suffix: `*.min.js`, `*.min.mjs`, `*.min.cjs`, `*.min.jsx`, `*.min.ts`, `*.min.tsx`, `*.min.css`.
- **Source maps** by suffix: `*.map`.

Per-symbol confidence (#34) carries the gradient for everything else (vendor/, README, generated markers); the static blocklist is preserved as a hard pre-filter only for files where extraction would be wasted work.

The skip count is reported in the indexer's structured log line as `blocked=N` and on `IndexResult.Blocked` for programmatic callers.

### Refusing obvious bloat traps

`pincher index <path>` refuses two catastrophic targets in any mode — the filesystem root (`/` on Linux/macOS, `C:\` on Windows, detected as any path that is its own parent) and the user's home directory (`$HOME` / `%USERPROFILE%`, with symlinks resolved). Either mistake walks tens of GB of cache and package data and was the cause of the 70 GB WAL incident this guard addresses.

In **hook mode** (`pincher index --hook`), the guard tightens further: the target directory must contain at least one project marker (`.git`, `.hg`, `.svn`, `go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`, `Gemfile`, `pom.xml`, `build.gradle`, `build.gradle.kts`, `Makefile`, `CMakeLists.txt`). Manual `pincher index <path>` skips the marker check — explicit user action is treated as authoritative for any non-catastrophic path. The MCP `index` tool path goes through the same guard.

### Cross-process safety

Multiple pincher processes can safely share one data directory. Each `Index()` run acquires a per-project filesystem lockfile (`<dataDir>/locks/<project-id-hash>.lock`) before touching the database; concurrent indexers on the same project block at the file level instead of fighting over the SQLite WAL. Stale lockfiles are reclaimed automatically when (a) the holder PID is no longer alive, (b) the lock is older than 24 hours, or (c) the payload is corrupt. This is what keeps a manual `pincher index` and a Claude Code SessionStart hook from racing each other.

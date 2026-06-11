Answer the following five questions about THIS repository (the pincher codebase you are
currently in). For each answer cite file paths and line numbers as evidence. Be concise —
a short paragraph or list per question. Do not modify any files.

1. The indexer has a "bloat trap" guard: what file-size cap does indexing enforce so that
   oversized files cannot bloat the index, where is that cap implemented, and which test
   covers the behaviour (see internal/server/index_bloat_trap_test.go for a starting
   point if needed)?

2. List the depth-1 (direct) callers of `db.Open` (defined in internal/db/db.go). For
   each caller give the function name and file.

3. The `trace` tool auto-trims its BFS depth when `depth` is omitted ("adaptive depth",
   reported via `_meta.depth_used`). Explain the trimming rule and name the tests that
   cover it.

4. Describe the wire format between the Go Python runner (internal/ast/python_runner.go)
   and the `python_extract.py` daemon: how are requests and responses framed, what fields
   do they carry, and how is the daemon's lifecycle (start/restart/shutdown) managed?

5. Does the indexer support a `.pincherignore` file today? Describe how file skipping
   currently works (which patterns/directories are excluded and where that logic lives),
   and what an implementation of `.pincherignore` would need to touch.

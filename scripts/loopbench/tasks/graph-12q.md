Answer the following twelve call-graph questions about THIS repository (the pincher
codebase you are currently in). These are who-calls / what-calls / blast-radius
questions. For each answer give function names plus file paths (line numbers where
useful). Be concise — a list per question. Do not modify any files.

1. List the direct (depth-1) callers of `db.Open` (internal/db/db.go).

2. List the direct callers of `(*Store).BulkUpsertSymbols` (internal/db/db.go).

3. Roughly how many call sites does `(*Server).jsonResultWithMeta`
   (internal/server/server.go) have? Name five of the handler functions that call it.

4. List the direct callers of `db.ApproxTokens` (internal/db/db.go) inside
   internal/server (non-test code).

5. What functions does `(*Server).handleContext` (internal/server/server.go) call
   directly (depth-1 outbound)? Name the five most significant.

6. What functions does `(*Server).handleTrace` (internal/server/server.go) call
   directly (depth-1 outbound)? Name the five most significant.

7. How is `(*Server).handleChanges` (internal/server/server.go) reached — where is it
   registered/dispatched from?

8. Which test functions exercise `(*Server).handleChanges`? Name at least three test
   files that cover the changes handler.

9. Which test functions cover the trace handler's adaptive-depth behaviour
   (`_meta.depth_used`)?

10. Blast radius: if the signature of `(*Store).BulkUpsertSymbols` changed, which
    packages (directories) would need updating? Justify from its callers.

11. What does `(*Indexer).Index` (internal/index/indexer.go) call directly (depth-1
    outbound)? Name the main phases/helpers it invokes.

12. Which functions reach `db.ApproxTokens` transitively within two hops (depth-2
    inbound), beyond the direct callers from question 4? Name a few and their hop depth.

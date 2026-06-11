Answer the following eight questions about THIS repository (the orderflow polyglot
codebase you are currently in: a Go API service with sharded order verticals under
internal/pkgNN/, Python fulfillment workers under workers/, and a TypeScript
storefront under web/). For each answer cite file paths (line numbers where useful)
as evidence. Be concise — a short list or paragraph per question. Do not modify any
files.

Beware: this repo contains generated artifacts (gen/, fixtures/, web/dist/) and many
near-identical shard packages whose functions differ only by their pkgNN suffix. Be
precise about WHICH function or value you mean, and in which file.

1. The storefront dispatches the "pkg07/submit" action. Which Python function
   ultimately fulfills that order? Trace the full cross-language path
   (TS → Go → queue → Python), naming each function/file hop and the string keys
   that connect the layers.

2. Exactly one function performs the payment capture (charging the card) for a
   pkg21-vertical order submitted through the live pipeline. Which function, in
   which file?

3. Is `ProcessOrderPkg33V1` (internal/pkg33/dead_v1.go) called or registered
   anywhere in non-test code? Answer dead or alive, with the evidence you checked.

4. The Python module workers/handlers/pkg14_legacy.py defines a `fulfill_pkg14`
   function. Can the worker ever execute it for a queued job? Explain exactly why
   or why not, citing the registration mechanism.

5. Blast radius: if the signature of `SaveRecordPkg12` (internal/pkg12/store.go)
   changed, list every non-test call site (function + file) that would need
   updating.

6. List the direct (depth-1) non-test callers of `RecordAuditPkg30`
   (internal/pkg30/store.go) — the function names and their files.

7. What is the effective default value of the `pkg26_retry_limit` configuration
   knob, in which file is that default defined, which Go function consumes the
   knob, and which function calls THAT consumer? (Careful: several files contain
   plausible-looking values for it.)

8. How many job types does the Python worker register at startup in total, and
   which handler function (module + name) fulfills pkg09-vertical orders?

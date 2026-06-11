# Pincher-via-curl coaching (DEPRECATED arm — comparison only)

A pincher code-intelligence server is running at `http://127.0.0.1:7878`. Use it via
curl instead of Grep/Glob/Read for code-navigation questions.

- Call a tool: `curl -s -X POST http://127.0.0.1:7878/v1/<tool> -H 'Content-Type: application/json' -d '<json-args>'`
- Useful tools: `search` (`{"query":"Name"}`), `symbol` (`{"id":"<id from search>"}`),
  `symbols` (`{"ids":[...]}`), `context` (`{"name":"Name"}`), `trace`
  (`{"name":"Name","direction":"inbound","depth":1}`).
- Pipe responses through `jq` to keep only the fields you need, e.g.
  `| jq '.hops[] | {from, to, depth}'` or `| jq '.results[] | {id, file_path, start_line}'`.
- Fall back to grep only when pincher returns nothing useful.
- Answer each question as soon as you have evidence.

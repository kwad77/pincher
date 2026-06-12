## Routing (pincher-router detected)
Maker-stage task units route through `route` before subagent spawn — the
pincher-loop skill's dispatch verse is authoritative for when/how. The S5
gate never routes below the originating tier. Routed output is untrusted
until gated.

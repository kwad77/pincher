# README positioning audit — 2026-06-09

## Verdict

The README was technically dense but undersold the actual product. It described Pincher as a codebase intelligence server, then immediately jumped into implementation details. The stronger pitch is: Pincher is local graph intelligence for LLM coding agents. It changes the agent loop from raw file spelunking to source-grounded, low-token, routing-aware tool calls.

## Main problems found

1. The core value was buried.
   - The README led with token savings, but not with the agent behavior change: search → context → trace → changes.
   - It did not clearly say that Pincher turns navigation into structured evidence with provenance.

2. The routing story was too subtle.
   - `_meta` was present, but framed as metadata rather than the thing that lets hosts and Pincher Router make better model/tool decisions.
   - The README did not connect `complexity_tier`, `tokens_saved_pct`, warnings, and `next_steps` to routing leverage.

3. Some claims sounded too salesy or hard to verify.
   - "80x+" was memorable but easy to question without the baseline method right beside it.
   - Savings should stay falsifiable: raw token inputs, baseline method, and realistic ranges by workflow.

4. The current release section was stale.
   - README still called out v0.98/v0.99/v0.90 positioning even though GitHub Release `v1.0.0` exists.

5. The narrative was not differentiated enough.
   - "Codebase intelligence server" could describe a lot of tools.
   - The sharper differentiator is the combination of local deterministic extraction, graph tools, provenance, and `_meta` evidence for routing.

6. New v1.2 direction was absent.
   - Recent work added `pincher report` rationale grouping, and the v1.2 roadmap is about graph intelligence/routing leverage. The README did not mention this direction at all.

## Rewrite strategy

- Lead with the agent-loop problem: raw Read/Grep wastes context and gives no routing signal.
- State Pincher's product boundary plainly: local graph intelligence for agents, not a search UI and not an LLM extraction pipeline.
- Make the evidence loop concrete: `search`, `context`, `trace`, `changes`, `report`, `_meta`.
- Keep savings claims auditable and avoid dollar estimates.
- Move implementation internals lower, after the user understands why they care.
- Update release status to v1.0.0 and make v1.2 an honest roadmap note.
- Preserve quickstart and host setup links, but make the first successful path shorter.

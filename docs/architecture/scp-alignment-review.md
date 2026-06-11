# SCP-aligned envelope — strategic review (#1397)

**Status:** Review / spike outcome (v1.3). Not a commitment to implement.
**Decision-maker:** kwad77 (sole maintainer through v1.x)
**Issue:** [#1397](https://github.com/kwad77/pincher/issues/1397)
**Relates to:** [ADR-0002](../adr/0002-v1-frozen-surface.md) frozen surface · [ADR-0005](../adr/0005-v1.3-substrate-and-language-coverage.md) §7 strategic spike · [`docs/integrations/meta-envelope-contract.md`](../integrations/meta-envelope-contract.md)

## Why this exists

The maintainer runs a sibling private-repo family — **Stoa / Ordo / Atrium / Gallery / Bridge / Runner / Cortex** — that already standardizes an event shape called **SCP (Stoa Component Protocol)**:

```go
// kwad77/ordo internal/envelope/envelope.go
type Envelope struct {
    V          int         `json:"v"`
    ID         string      `json:"id"`         // ULID
    TS         string      `json:"ts"`
    Component  string      `json:"component"`
    Kind       string      `json:"kind"`
    RunID      string      `json:"runId"`
    ParentID   string      `json:"parentId,omitempty"`
    Provenance *Provenance `json:"provenance,omitempty"`
    Payload    any         `json:"payload,omitempty"`
}
```

Pincher's `_meta` envelope is designed independently. If the two stay independent, any Stoa-family component that wants to consume pincher output must write a translator. If they align where it is cheap to do so, the family gets pincher integration closer to free. This review answers the four questions #1397 poses, **without changing any `_meta` field** — alignment, if pursued, must ride additive `*_v3` extension points per ADR-0002, never redefine an existing field.

## 1. Where the two map cleanly

| SCP `Envelope` field | pincher source | Mapping quality | Notes |
|---|---|---|---|
| `ID` (ULID) | `_meta.request_id` (ULID) | **Exact** | Both already ULIDs. A consumer can copy `request_id → ID` with zero transformation. |
| `TS` | response emission time / `_meta.latency_ms` anchor | **Derivable** | pincher does not currently emit an absolute `ts` in `_meta`; it emits `latency_ms`. An additive `_meta.ts` (RFC-3339) would close this exactly. Cheap. |
| `Component` | `_meta.capabilities` + server `Implementation{Name:"pincher",Version}` | **Strong** | SCP `Component` is a name+version identity; pincher already carries `schema_vN` + the binary version. A flat `component:"pincher@<ver>"` projection is mechanical. |
| `Kind` | per-tool result kind (tool name + result shape) | **Strong** | SCP `Kind` is the event discriminator. pincher's tool name (`search`, `trace`, …) plus an outcome facet (`ok` / `empty` / `partial`) is the natural `Kind`. Not currently emitted as a single field; derivable from `result` + `empty_reason`. |
| `Payload` | `result` (tool payload) **or** `/v1/events` SSE event body | **Exact (SSE)** / **Strong (tool)** | The SSE lane (`/v1/events`, capability `sse`, #654) already emits structured event bodies that map onto `Payload` directly. For tool responses, `result` is the payload. |
| `Provenance` | `_meta.binary_drift_warning`, `schema_version`, `schema_version_at_index`, and (new in v1.3) `edges.provenance_tier` | **Partial → Strong** | pincher has the *substance* of provenance (which binary/schema produced this, EXTRACTED vs inferred edges) but scattered across fields rather than a single `provenance` object. The v1.3 provenance-tier substrate is the natural anchor for a consolidated projection. |

## 2. Where they diverge intentionally

| Concern | pincher `_meta` | SCP `Envelope` | Why the divergence is correct |
|---|---|---|---|
| **Primary purpose** | Planning-loop **input** for the *calling agent* (cost accounting, steering, empty-reason diagnosis). | Event-stream **record** for *downstream observers* (correlation, replay, lineage). | Different audiences. `_meta` is request/response-scoped and optimized for the agent's next decision; SCP is fire-and-forget telemetry. Forcing one shape onto both would bloat `_meta` with fields no agent reads. |
| **Cost accounting** | First-class: `tokens_used`, `tokens_saved`, `tokens_saved_pct`, `baseline_method`, `complexity_tier`, `latency_ms`. | Absent (lives in `Payload` if at all). | pincher's token-savings story is its core value prop and must stay top-level in `_meta`. It does **not** belong in the SCP envelope header — it would map into `Payload` for an emitted event. |
| **Run lineage** | No `RunID` / `ParentID`. pincher has `session_id` + `request_id` but no parent-chaining. | `RunID` + `ParentID` model a call tree. | pincher tool calls are not a tree today — each is independent within a session. Adopting `RunID`/`ParentID` would be **new semantics**, not a renaming. Out of scope unless pincher gains multi-step orchestrated runs. |
| **Steering** | `next_steps`, `warnings_v2`, `diagnosis_v2` (code+severity+message+data). | Not modeled. | These are agent-facing affordances unique to pincher's planning-loop role. They stay pincher-specific. |
| **Versioning** | `schema_version` (DB schema) + `capabilities[schema_vN]`. | `V` (envelope-format version). | Different axes: pincher's `schema_version` is about the *indexed data*; SCP `V` is about the *envelope format*. A projection needs its own `V`, independent of pincher's schema version. |

## 3. What alignment would cost

Three tiers, cheapest first. None requires breaking `_meta`.

1. **Projection adapter (zero pincher change).** A Stoa-family consumer maps pincher output → SCP at ingest: `ID←request_id`, `Component←"pincher@ver"`, `Payload←result|sse-body`, synthesize `TS`/`Kind`. Cost lives entirely in the family repo. **Recommended default** — it costs pincher nothing and the mapping is mechanical for everything except `Provenance` and `Kind`.
2. **Additive `_meta` v3 fields (small pincher change).** Emit `_meta.ts` (RFC-3339), `_meta.kind` (tool+outcome discriminator), and a consolidated `_meta.provenance` object (folding `binary_drift_warning` + `schema_version*` + dominant `provenance_tier`). All pure additions under the ADR-0002 `*_v3` extension allowance; existing fields keep emitting unchanged. This removes the synthesize-on-ingest work from tier 1 and makes the projection 1:1. Cost: a handful of fields + contract-doc rows + a gate test. **The right move if ≥2 family components actually consume pincher.**
3. **Native SCP emission on `/v1/events` (larger).** Have the SSE lane emit SCP envelopes directly (opt-in via a content-negotiation header or a `?format=scp` query param), reusing the existing event source. Highest fidelity, but commits pincher to the SCP `V` contract and a translation layer it must version alongside SCP. Only justified once the family dependency is real and stable. **Defer until tier 2 is in production and the perf gate (below) is green.**

## 4. Hard perf gates (per #1397 and ADR-0005 §42)

Any tier-2/tier-3 work must not regress the `_meta` envelope's measured characteristics. Gates to enforce before merging envelope changes:

- **Latency:** added `_meta` fields must not increase median tool-response `latency_ms` by more than **2%** on the standard corpus bench (`cmd/pinch` report-fidelity corpora). `ts`/`kind`/`provenance` are cheap string/struct assembles — expected delta ≈ 0.
- **Token cost:** the per-response `_meta` byte budget must not grow enough to erode `tokens_saved_pct`. Budget the three additive fields at **< 120 bytes** total; gate via a serialized-size assertion in the `_meta` contract test. (For aggregators that already trim `_meta`, the existing `PINCHER_META_CAPABILITIES=off` / short-description opt-outs apply.)
- **No new allocation on the hot path:** the consolidated `provenance` object must be assembled from already-loaded values (no extra query). Pin with a bench that asserts zero added DB round-trips per tool call.

## Decision

- **v1.3:** record this review (this document). **No `_meta` change ships in v1.3.** The provenance-tier substrate that already landed is the precondition that makes a future consolidated `_meta.provenance` projection clean — that is the v1.3 contribution to this thread.
- **Default posture:** tier 1 (consumer-side projection adapter). pincher owes the family nothing structurally; the mapping is mechanical for `ID`/`Component`/`Payload`.
- **Re-open trigger for tier 2:** ≥ 2 Stoa-family components (Ordo/Atrium/etc.) in active use consuming pincher output AND a concrete translator-maintenance pain report. At that point ship the additive `_meta.ts` + `_meta.kind` + `_meta.provenance` fields behind the perf gates above. Tier 3 (native SCP on SSE) stays deferred until tier 2 is proven in production.

## Open questions for the maintainer

These are cross-repo strategic calls outside pincher's own scope:

1. Is the Stoa-family dependency on pincher real enough in the next two minors to justify tier 2, or does the projection adapter (tier 1) suffice indefinitely?
2. Should `RunID`/`ParentID` lineage ever enter pincher — i.e., will pincher gain multi-step orchestrated runs where call-tree correlation matters — or does `session_id` + `request_id` remain sufficient?
3. If tier 3 is ever pursued, does pincher pin to a specific SCP `V`, or negotiate it per-connection?

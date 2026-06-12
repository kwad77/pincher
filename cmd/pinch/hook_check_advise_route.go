// SPDX-License-Identifier: MIT

package main

import (
	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/server"
)

// advise_route (router-loop plan §A2 / item B8): recruit the router.
// An exact clone of the shipped advise_index mechanism (#2016) on a
// new trigger tool. The measured hook lesson (#2014: 726 invocations,
// 0 redirects) is that PreToolUse hooks excel at one-time recruitment
// advisories, not in-band decisions — and the hook is the only
// mechanism that observes the NATIVE loop. When a session inside a
// router-equipped machine keeps spawning subagents (Task events)
// without the routing surface in play, the highest-value advisory is
// "the route tool exists and the pincher-loop dispatch verse governs
// when to consult it" — converting an un-recruited session into a
// skill-recruited one exactly once, at zero blocking risk.
//
// The trigger is the plan's, verbatim (§A2): (i) router capability
// detected at hook time via detection-ladder rungs 1–2 ONLY (config
// stat + LookPath — no network inside the <50ms budget; an installed-
// but-idle router is exactly the state worth recruiting), (ii) ≥2
// prior Task events this session (hook_invocations rows — the same
// free counter #2016 uses; the matcher gained `Task` so those rows
// exist), and (iii) no advise_route row for this session yet.
//
// HONEST GAP (documented deviation-in-spirit, not in letter): the
// plan's framing — "spawning subagents WITHOUT consulting the router"
// — implies observing the session's `route` calls. The hook cannot:
// MCP tool traffic is not on the PreToolUse matcher, and the server's
// own tool-call telemetry is keyed by MCP session ids, not the Claude
// Code session_id this payload carries — there is no honest join at
// decision time. So "without consulting" is approximated by the
// once-per-session guarantee: worst case, a session that already
// routes sees one redundant one-line advisory. The adoption signal
// (advise_route → route-consult conversion) is measured offline by
// joining hook_invocations against the router's outcomes.jsonl, where
// the session-id domains CAN be reconciled (plan §A2 adoption signal).
//
// Persistence + telemetry choices mirror #2016 exactly: the threshold
// counter is the rows prior invocations wrote, the suppression check
// is the existence of a prior advise_route row, and the advisory row
// uses the distinct decision value "advise_route" so it never pollutes
// the redirect conversion metrics (which filter on decision IN
// ('redirect','redirect_advisory')). The file_path column carries the
// SESSION KEY (plan §A2) — the suppression key and the take-rate join
// key. took_recommendation stays NULL on purpose, like advise_index:
// the expected take is a `route` MCP call the hook-side joiner cannot
// observe, and a false "saw it and rejected it" would be worse than
// no resolution.

// adviseRouteThreshold is the per-session count of Task (subagent
// spawn) events at which the one-time advisory fires. 3 — mirroring
// adviseIndexThreshold's rationale: one spawn can be incidental, two
// can be a quick fan-out; three subagent spawns in one session on a
// router-equipped machine is a working loop that routing would have
// served. Equals the plan's "≥2 prior Task events" + the current one.
const adviseRouteThreshold = 3

// hookRouterInstalled is the rungs-1–2 detection seam. Production
// value is server.RouterInstalledNoProbe (config stat + LookPath, no
// network — see router_detect.go); tests substitute it so the
// decision logic is testable independent of the machine's PATH and
// home directory. The PINCHER_ROUTER env contract rides along: off
// (and typos — fail direction absent) never advises, on forces the
// installed answer.
var hookRouterInstalled = server.RouterInstalledNoProbe

// decideTaskSpawn handles PreToolUse on the Task tool (subagent
// spawn). Emits the one-time advise_route advisory when ALL of:
//
//   - the session is identified (no session id → can't honor
//     once-per-session, so never advise);
//   - a pincher-router installation is present (detection-ladder
//     rungs 1–2 only; PINCHER_ROUTER=off ⇒ never);
//   - no advise_route row exists yet for this session;
//   - this is at least the adviseRouteThreshold-th Task event this
//     session (prior rows + this event).
//
// Advisory-only by construction: every return has Continue=true; the
// advisory differs from plain pass-through only in the systemMessage
// and the telemetry row. The Task call always passes through — a
// recruitment hint must never be able to block a subagent spawn.
func decideTaskSpawn(store *db.Store, in hookCheckInput, debug bool) hookDecision {
	if in.SessionID == "" {
		return debugPass(debug, "task spawn (no session id — once-per-session unenforceable, never advise)", hookDecision{})
	}
	if !hookRouterInstalled() {
		return debugPass(debug, "task spawn (no pincher-router installed — rungs 1–2 absent)", hookDecision{})
	}
	if store.HookRouteAdvisedInSession(in.SessionID) {
		return debugPass(debug, "route advisory already emitted this session", hookDecision{})
	}
	// Prior Task events this session, from the rows earlier invocations
	// logged. +1 for the current event, which is not logged yet when
	// the decision runs — same accounting as advise_index.
	if store.HookTaskEventsInSession(in.SessionID)+1 < adviseRouteThreshold {
		return debugPass(debug, "task spawn below route-advisory threshold", hookDecision{})
	}

	// Plan §A2's advisory text, verbatim.
	msg := "Pincher hint: pincher-router is installed and this session is spawning subagents without consulting it. The pincher-loop skill's dispatch verse routes maker work to the cheapest worker that clears the gate; `route` is on the MCP surface. (One-time advisory — Task passes through.)"
	return hookDecision{
		Continue:      true,
		SystemMessage: msg,
		Decision:      "advise_route",
		SuggestedTool: "route",
		// The advisory row's file_path carries the SESSION KEY (plan
		// §A2): the once-per-session suppression key for
		// HookRouteAdvisedInSession and the join key for the
		// advise_route → route-consult take-rate query.
		FilePathParsed: in.SessionID,
	}
}

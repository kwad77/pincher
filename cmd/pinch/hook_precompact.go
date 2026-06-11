// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// PreCompact handler (precompact-hook): ledger-aware compaction
// advisories.
//
// The symbolization→window-shrink law: when the harness summarizes a
// long conversation, durable facts already checkpointed in the loop
// ledger / ADR store can be dropped to pointers — but only if the
// summarizer KNOWS what is recoverable. This handler tells it.
//
// Contract (mirrors the PreToolUse path's defensive patterns):
//   - fail-open silently, always `{"continue": true}` — a hook bug must
//     never break compaction (#1654 advisory-mode lineage)
//   - <50ms latency budget; ≤3 read queries against the store
//     (ListProjects → LoopLedgerStats → CountADRs)
//   - empty ledger (no loops AND no ADRs) → zero noise: bare
//     pass-through with no advisory chrome
//   - telemetry rides the existing hook_invocations columns with
//     tool_name="compact" — no schema change

// precompactStore is the exact read surface the PreCompact handler is
// allowed to touch — three methods, one query each. Narrow on purpose:
// the ≤3-query budget is enforced structurally (a counting test wraps
// this interface) rather than by convention. *db.Store satisfies it.
type precompactStore interface {
	ListProjects() ([]db.Project, error)
	LoopLedgerStats(projectID string) ([]db.LoopLedgerStat, error)
	CountADRs(projectID string) (int, error)
}

// precompactReceiptMaxRunes truncates the latest-checkpoint receipt in
// the advisory, matching the 80-rune receipt cap in loop_tool.go.
const precompactReceiptMaxRunes = 80

// matchProjectForDir resolves a directory to an indexed project using
// the same longest-path-prefix rule matchIndexedFile applies to file
// paths (nested projects win). The PreCompact payload carries the
// session's cwd, not a file path, so the match is directory-shaped:
// cwd equal to, or anywhere under, the project root.
func matchProjectForDir(projects []db.Project, dir string) (db.Project, bool) {
	if dir == "" {
		return db.Project{}, false
	}
	clean := filepath.Clean(dir)
	var best db.Project
	bestLen := -1
	for _, p := range projects {
		base := filepath.Clean(p.Path)
		if clean != base && !strings.HasPrefix(clean, base+string(filepath.Separator)) {
			continue
		}
		if len(base) > bestLen {
			best, bestLen = p, len(base)
		}
	}
	return best, bestLen >= 0
}

// decidePreCompact builds the ledger advisory for a PreCompact event.
// Every miss in the chain (no cwd, no matching project, store errors,
// empty ledger) degrades to a silent pass-through — the hook informs,
// it never blocks and never guesses.
func decidePreCompact(store precompactStore, in hookCheckInput, debug bool) hookDecision {
	projects, err := store.ListProjects()
	if err != nil {
		return debugPass(debug, "list projects: "+err.Error(), hookDecision{})
	}
	proj, ok := matchProjectForDir(projects, in.CWD)
	if !ok {
		return debugPass(debug, "cwd not in any indexed project", hookDecision{FilePathParsed: in.CWD})
	}

	loops, err := store.LoopLedgerStats(proj.ID)
	if err != nil {
		return debugPass(debug, "loop ledger stats: "+err.Error(), hookDecision{FilePathParsed: in.CWD})
	}
	adrCount, err := store.CountADRs(proj.ID)
	if err != nil {
		return debugPass(debug, "adr count: "+err.Error(), hookDecision{FilePathParsed: in.CWD})
	}

	if len(loops) == 0 && adrCount == 0 {
		// Empty ledger → zero noise. An advisory about nothing trains
		// the summarizer (and the user) to ignore the hook.
		return debugPass(debug, "ledger empty — nothing recoverable to advertise",
			hookDecision{FilePathParsed: in.CWD})
	}

	msg := precompactAdvisory(loops, adrCount)
	d := hookDecision{
		Continue:       true,
		SystemMessage:  msg,
		Decision:       "ledger_advisory",
		FilePathParsed: in.CWD,
	}
	if len(loops) > 0 {
		d.SuggestedTool = "loop"
		d.SuggestedArgs = fmt.Sprintf(`{"action":"resume","name":%q}`, loops[0].LoopName)
	} else {
		d.SuggestedTool = "adr"
		d.SuggestedArgs = `{"action":"list"}`
	}
	return d
}

// precompactAdvisory renders the one advisory line the compactor sees.
// Shape: what is checkpointed, then the instruction — prefer pointers
// over payload reproduction, everything is recoverable.
func precompactAdvisory(loops []db.LoopLedgerStat, adrCount int) string {
	var b strings.Builder
	b.WriteString("Durable state for this project lives in pincher: ")

	openTriggers := 0
	for _, l := range loops {
		openTriggers += l.OpenTriggers
	}

	var frags []string
	if len(loops) > 0 {
		lead := loops[0]
		receipt := lead.LatestReceipt
		if r := []rune(receipt); len(r) > precompactReceiptMaxRunes {
			receipt = string(r[:precompactReceiptMaxRunes-3]) + "..."
		}
		loopFrag := fmt.Sprintf("loop '%s' (%d checkpoints, latest: %s#%d %s)",
			lead.LoopName, lead.Checkpoints, lead.LoopName, lead.LatestSeq, receipt)
		if extra := len(loops) - 1; extra > 0 {
			loopFrag += fmt.Sprintf(" +%d more loop(s)", extra)
		}
		frags = append(frags, loopFrag)
		frags = append(frags, fmt.Sprintf("%d open reopen-triggers", openTriggers))
	}
	frags = append(frags, fmt.Sprintf("%d ADRs", adrCount))
	b.WriteString(strings.Join(frags, ", "))

	b.WriteString(". Prefer pointers (<loop>#<seq>, ADR keys, symbol ids) over payload reproduction in the summary; everything is recoverable via loop resume / adr get.")
	return b.String()
}

// emitPreCompactResponse writes the PreCompact hook response. Silent
// pass-through emits the bare `{"continue": true}` envelope (same as
// emitHookResponse); an advisory carries the line on BOTH channels the
// hooks contract offers — systemMessage (user-visible) and
// hookSpecificOutput.additionalContext (injected into the compaction
// context, mirroring the SessionStart envelope `pincher index --hook`
// emits) — so whichever channel the harness honors for PreCompact, the
// summarizer sees the advisory.
func emitPreCompactResponse(d hookDecision) {
	resp := map[string]any{"continue": d.Continue}
	if d.SystemMessage != "" {
		resp["systemMessage"] = d.SystemMessage
		resp["hookSpecificOutput"] = map[string]any{
			"hookEventName":     "PreCompact",
			"additionalContext": d.SystemMessage,
		}
	}
	out, _ := json.Marshal(resp)
	os.Stdout.Write(out)
	os.Stdout.Write([]byte("\n"))
}

// logPreCompactInvocation counts the firing in hook_invocations using
// the existing columns — tool_name takes the distinct value "compact"
// (the event has no tool), file_path holds the session cwd. Best-effort
// like logHookDecision: a failed insert never blocks the response.
func logPreCompactInvocation(store *db.Store, in hookCheckInput, d hookDecision) {
	_ = store.LogHookInvocation(db.HookInvocation{
		TS:            time.Now().UnixNano(),
		SessionID:     in.SessionID,
		ToolName:      "compact",
		FilePath:      d.FilePathParsed,
		Decision:      d.Decision,
		SuggestedTool: d.SuggestedTool,
		SuggestedArgs: d.SuggestedArgs,
	})
}

// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PR-8/9 (loop-substrate): the loop ledger + resume brief.
//
// The biggest cost of a long agent loop is not any single call — it is
// that the loop's working state (claims, decisions, reopen triggers,
// "where was I") lives in the transcript, which dies with the context
// window. The ledger moves that state into pincher: `checkpoint`
// appends one EGDL iteration's record; `resume` composes the tail into
// ONE bounded brief (default ~800 tokens) so a fresh session — or a
// different model — picks up mid-flight work in a single call.
//
// ADR remains the home for *conventions* (timeless per-project
// knowledge); the ledger is for *in-flight work* (ordered, append-only,
// watermark-stamped).

// loopResumeDefaultBudget bounds the resume brief when the caller
// doesn't pass max_tokens. Small on purpose: the brief is a pointer
// into the work, not the work itself.
const loopResumeDefaultBudget = 800

// loopWatermark renders the current index generation ("g3") or "" when
// the server has no indexer (unit-test servers, read-only mounts).
func (s *Server) loopWatermark() string {
	if s.indexer == nil {
		return ""
	}
	return fmt.Sprintf("g%d", s.indexer.Generation())
}

func (s *Server) handleLoop(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	projectID, errRes := s.mustProject(args)
	if errRes != nil {
		return errRes, nil
	}
	action := str(args, "action")
	name := str(args, "name")

	switch action {
	case "start", "checkpoint":
		if name == "" {
			return s.errResultRich("loop "+action+" requires `name`", []map[string]string{
				{"tool": "loop", "args": `{"action":"start","name":"rollout-x","claim":"ship feature X behind a flag"}`,
					"why": "open a named loop with its framing claim"},
				{"tool": "loop", "args": `{"action":"list"}`,
					"why": "see which loops already exist before starting a duplicate"},
			}), nil
		}
		cp := db.LoopCheckpoint{
			ProjectID:     projectID,
			LoopName:      name,
			Claim:         str(args, "claim"),
			Decision:      str(args, "decision"),
			Confidence:    str(args, "confidence"),
			ReopenTrigger: str(args, "reopen_trigger"),
			Evidence:      str(args, "evidence"),
			Watermark:     s.loopWatermark(),
		}
		if action == "start" && cp.Decision == "" {
			cp.Decision = "started"
		}
		seq, err := s.store.AppendLoopCheckpoint(cp)
		if err != nil {
			return errResult(fmt.Sprintf("loop %s: %v", action, err)), nil
		}
		// Eviction-shaped receipt: one canonical line a context-window
		// summarizer naturally keeps while dropping the payload it
		// refers to. The window holds the pointer table; the ledger
		// holds the payloads — re-derivable via `loop resume`.
		receiptLabel := cp.Claim
		if receiptLabel == "" {
			receiptLabel = cp.Decision
		}
		if r := []rune(receiptLabel); len(r) > 80 {
			receiptLabel = string(r[:77]) + "..."
		}
		data := map[string]any{
			"loop":      name,
			"seq":       seq,
			"watermark": cp.Watermark,
			"receipt":   fmt.Sprintf("%s#%d stored: %s — evict the payload; recover via loop resume", name, seq, receiptLabel),
			"_meta": map[string]any{
				"next_steps": []map[string]string{
					{"tool": "loop", "args": fmt.Sprintf(`{"action":"resume","name":%q}`, name),
						"why": "one bounded brief recovers this loop's state in a fresh session"},
				},
			},
		}
		return s.jsonResultWithMeta(data, start, tool, args, 0), nil

	case "handoff":
		// M17: compose the pointer manifest server-side and append it
		// as a checkpoint — replaces prose handoff.md (loop_handoff.go).
		return s.loopHandoff(ctx, start, tool, args, projectID, name), nil

	case "export":
		// M17: render the ledger as a human-readable Markdown document
		// on demand. Never writes files (loop_handoff.go).
		return s.loopExport(start, tool, args, projectID, name), nil

	case "list":
		if name == "" {
			loops, err := s.store.ListLoops(projectID, intArg(args, "limit", 20))
			if err != nil {
				return errResult(fmt.Sprintf("loop list: %v", err)), nil
			}
			if loops == nil {
				loops = []db.LoopSummary{}
			}
			return s.jsonResultWithMeta(map[string]any{"loops": loops}, start, tool, args, 0), nil
		}
		entries, err := s.store.ListLoopCheckpoints(projectID, name, intArg(args, "limit", 10))
		if err != nil {
			return errResult(fmt.Sprintf("loop list: %v", err)), nil
		}
		if entries == nil {
			entries = []db.LoopCheckpoint{}
		}
		return s.jsonResultWithMeta(map[string]any{"loop": name, "checkpoints": entries}, start, tool, args, 0), nil

	case "resume":
		if name == "" {
			loops, err := s.store.ListLoops(projectID, 1)
			if err != nil || len(loops) == 0 {
				return s.errResultRich(
					fmt.Sprintf("no loops recorded for project %q — nothing to resume", projectID),
					[]map[string]string{
						{"tool": "loop", "args": `{"action":"start","name":"<loop-name>","claim":"<what this loop ships>"}`,
							"why": "open the loop first; resume only has meaning once checkpoints exist"},
						{"tool": "adr", "args": `{"action":"list"}`,
							"why": "prior sessions may have captured state as ADRs instead"},
					},
				), nil
			}
			name = loops[0].LoopName
		}
		entries, err := s.store.ListLoopCheckpoints(projectID, name, 20)
		if err != nil {
			return errResult(fmt.Sprintf("loop resume: %v", err)), nil
		}
		if len(entries) == 0 {
			return s.errResultRich(
				fmt.Sprintf("loop %q has no checkpoints in project %q", name, projectID),
				[]map[string]string{
					{"tool": "loop", "args": `{"action":"list"}`,
						"why": "see which loops exist and how recently each was touched"},
				},
			), nil
		}

		budget := maxTokensArg(args)
		if budget <= 0 {
			budget = loopResumeDefaultBudget
		}
		// Newest-first inclusion under the budget; ship oldest-first
		// for reading order. Entry cost is approximated on its text
		// fields plus a fixed envelope overhead.
		included := []db.LoopCheckpoint{}
		used := 0
		for _, e := range entries {
			cost := db.ApproxTokens(e.Claim+e.Decision+e.Evidence+e.ReopenTrigger) + 25
			if used+cost > budget && len(included) > 0 {
				break
			}
			included = append(included, e)
			used += cost
		}
		sort.Slice(included, func(i, j int) bool { return included[i].Seq < included[j].Seq })
		// M17: a handoff checkpoint is the loop's pointer manifest —
		// when the newest checkpoint is a handoff, it LEADS the brief
		// so a fresh session reads the manifest before the iteration
		// tail. (entries[0] is always included, so after the ascending
		// sort the newest sits at the end.)
		if n := len(included); n > 1 && isLoopHandoff(included[n-1]) {
			h := included[n-1]
			copy(included[1:], included[:n-1])
			included[0] = h
		}

		openTriggers := []map[string]any{}
		for _, e := range entries { // full window, not just included
			if strings.TrimSpace(e.ReopenTrigger) != "" {
				openTriggers = append(openTriggers, map[string]any{
					"seq": e.Seq, "reopen_trigger": e.ReopenTrigger,
				})
			}
		}

		// Did the symbol graph move since the newest checkpoint?
		wmNow := s.loopWatermark()
		indexChanged := false
		if last := entries[0].Watermark; last != "" && wmNow != "" && last != wmNow {
			indexChanged = true
		}

		adrKeys := []string{}
		if adrs, err := s.store.ListADRs(projectID); err == nil {
			for k := range adrs {
				adrKeys = append(adrKeys, k)
			}
			sort.Strings(adrKeys)
			if len(adrKeys) > 15 {
				adrKeys = adrKeys[:15]
			}
		}

		data := map[string]any{
			"loop":                                name,
			"brief":                               included,
			"open_triggers":                       openTriggers,
			"adr_keys":                            adrKeys,
			"watermark_now":                       wmNow,
			"index_changed_since_last_checkpoint": indexChanged,
			"omitted_checkpoints":                 len(entries) - len(included),
			"_meta": map[string]any{
				"next_steps": []map[string]string{
					{"tool": "changes", "args": `{"scope":"all"}`,
						"why": "see what the working tree holds before continuing the loop"},
					{"tool": "adr", "args": `{"action":"get","key":"<one of adr_keys>"}`,
						"why": "pull the convention/recipe entries the keys point at"},
				},
			},
		}
		// LES (ADR LOOP_EFFICIENCY_METRIC): one-line les_hint when this
		// loop's iteration_cost is computable from recorded telemetry —
		// the resume brief tells the agent what its iterations have
		// been costing before it starts the next one. Empty-decision
		// checkpoints never count (anti-gaming); no recorded tokens or
		// no counting checkpoints → no hint, never a guessed number.
		if hint := s.lesHintForLoop(projectID, name, time.Now()); hint != "" {
			data["les_hint"] = hint
		}
		if indexChanged {
			attachWarningStructured(data, "index_moved_since_checkpoint", WarningSeverityWarning,
				fmt.Sprintf("the index generation moved (%s → %s) since this loop's last checkpoint — cached symbol IDs/blast-radius from prior iterations may be stale; re-probe before editing", entries[0].Watermark, wmNow),
				map[string]any{"checkpoint_watermark": entries[0].Watermark, "watermark_now": wmNow})
		}
		return s.jsonResultWithMeta(data, start, tool, args, 0), nil

	default:
		return s.errResultRich(
			fmt.Sprintf("unknown loop action %q — accepted: start, checkpoint, handoff, list, resume, export", action),
			[]map[string]string{
				{"tool": "loop", "args": `{"action":"resume"}`,
					"why": "resume the most recently touched loop in one bounded brief"},
				{"tool": "loop", "args": `{"action":"list"}`,
					"why": "enumerate this project's loops"},
			},
		), nil
	}
}

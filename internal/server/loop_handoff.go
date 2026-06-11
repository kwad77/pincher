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

// M17 (loop-substrate): `loop handoff` + `loop export` — the pointer
// manifest that replaces prose handoff.md files.
//
// Measured motivation: a prose handoff costs 2-5k tokens to write,
// rots instantly (line numbers drift within ~3 iterations), and
// charges the next session the full re-read. The ledger's `resume`
// already reads pointers at ~150 tokens; handoff formalizes the WRITE
// side. A handoff is NOT a new storage shape — it is a regular
// checkpoint whose decision field carries a server-composed manifest
// of pointers (open triggers, ADR keys, working-tree summary, recent
// receipts, re-entry seeds), every one of which dereferences LIVE
// state on the next session instead of freezing prose. `export` is
// the escape hatch: prose only when a human actually asks for it,
// rendered on demand FROM the ledger and never written to disk.

// loopHandoffClaimPrefix marks a handoff checkpoint in the ledger.
// `resume` hoists the newest such checkpoint to the front of the
// brief so the manifest leads.
const loopHandoffClaimPrefix = "HANDOFF"

// loopHandoffDecisionBudget caps the manifest text stored in the
// handoff checkpoint's decision field, in approximate tokens. Small
// on purpose — the manifest is a pointer table, not the work.
const loopHandoffDecisionBudget = 600

// loopHandoffNoteMax caps the optional free-text note. Anything
// longer is prose creeping back in — the thing handoff exists to
// replace.
const loopHandoffNoteMax = 200

// loopExportDefaultBudget bounds the export document when the caller
// doesn't pass max_tokens.
const loopExportDefaultBudget = 2000

// loopHandoffWindow is how many trailing checkpoints handoff and
// export scan for triggers, receipts and seeds.
const loopHandoffWindow = 100

// loopAwaitingHumanMarker tags reopen triggers that block on a person,
// per the pincher-loop skill convention. Handoff carries these
// VERBATIM — truncating a question addressed to a human defeats it.
const loopAwaitingHumanMarker = "AWAITING HUMAN"

// isLoopHandoff reports whether a checkpoint is a handoff manifest.
func isLoopHandoff(cp db.LoopCheckpoint) bool {
	return strings.HasPrefix(cp.Claim, loopHandoffClaimPrefix)
}

// truncLoopText caps s at max runes with the same "..." style the
// checkpoint receipt uses.
func truncLoopText(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max-3]) + "..."
	}
	return s
}

// parseLoopSeedIDs extracts symbol-ID-shaped tokens
// ("<file>::<qualified>#<Kind>", the db.MakeSymbolID shape) from
// free-text evidence fields, scanning newest text first, deduped,
// capped at max. Best-effort by design: evidence is free text; a
// token that merely looks like an ID still points the next session at
// `symbols`, which rich-errors on a miss.
func parseLoopSeedIDs(texts []string, max int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, txt := range texts {
		for _, tok := range strings.FieldsFunc(txt, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';'
		}) {
			tok = strings.Trim(tok, "()[]{}\"'`")
			tok = strings.TrimRight(tok, ".:")
			sep := strings.Index(tok, "::")
			hash := strings.LastIndex(tok, "#")
			// Shape gate: non-empty file part, non-empty qualified
			// name between :: and #, non-empty kind after #.
			if sep <= 0 || hash < sep+3 || hash == len(tok)-1 {
				continue
			}
			if seen[tok] {
				continue
			}
			seen[tok] = true
			out = append(out, tok)
			if len(out) >= max {
				return out
			}
		}
	}
	return out
}

// composeLoopHandoffManifest renders the pointer manifest as compact
// text — the handoff checkpoint's decision field, and what the
// response echoes back so the writer sees exactly what the next
// session will. entries are newest-first (ListLoopCheckpoints order).
//
// Every line is a pointer into live state: triggers re-read from the
// ledger, ADR keys dereference via `adr get`, the tree summary
// re-derives via `changes scope=all`, seeds via `symbols`. Nothing
// here can rot the way prose line references do.
func (s *Server) composeLoopHandoffManifest(ctx context.Context, projectID, name string, entries []db.LoopCheckpoint) string {
	lines := []string{}

	if wm := s.loopWatermark(); wm != "" {
		lines = append(lines, "watermark: "+wm)
	}

	// Open reopen triggers, newest-first. AWAITING HUMAN entries ship
	// verbatim (a truncated question to a human is no question);
	// machine triggers trim at 160 runes. Cap 12 rendered, count the
	// rest — the ledger keeps the payloads.
	trig := []string{}
	total := 0
	for _, e := range entries {
		t := strings.TrimSpace(e.ReopenTrigger)
		if t == "" {
			continue
		}
		total++
		if len(trig) >= 12 {
			continue
		}
		if !strings.Contains(t, loopAwaitingHumanMarker) {
			t = truncLoopText(t, 160)
		}
		trig = append(trig, fmt.Sprintf("#%d %s", e.Seq, t))
	}
	if total > 0 {
		line := fmt.Sprintf("open[%d]: %s", total, strings.Join(trig, " | "))
		if total > len(trig) {
			line += fmt.Sprintf(" | +%d more", total-len(trig))
		}
		lines = append(lines, line)
	}

	// ADR keys (capped 15) — same projection resume ships; the keys
	// dereference via `adr get`.
	if adrs, err := s.store.ListADRs(projectID); err == nil && len(adrs) > 0 {
		keys := make([]string, 0, len(adrs))
		for k := range adrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 15 {
			keys = keys[:15]
		}
		lines = append(lines, fmt.Sprintf("adrs[%d]: %s", len(adrs), strings.Join(keys, ", ")))
	}

	// Working-tree summary via the SAME analysis `changes` runs
	// (scope=all, depth 1 — only counts + files are needed here).
	// Best-effort: a non-git root or missing indexer degrades to one
	// "unavailable" line, never an error.
	treeLine := "tree: unavailable"
	if s.indexer != nil {
		if root, rerr := s.resolveProjectRoot(projectID); rerr == nil {
			if an, aerr := AnalyzeChanges(ctx, s.store, s.indexer, projectID, root, "all", 1); aerr == nil {
				if len(an.ChangedFiles) == 0 {
					treeLine = "tree: clean"
				} else {
					files := an.ChangedFiles
					extra := ""
					if len(files) > 10 {
						extra = fmt.Sprintf(" +%d more", len(files)-10)
						files = files[:10]
					}
					treeLine = fmt.Sprintf("tree: %d dirty files, %d changed symbols — %s%s",
						len(an.ChangedFiles), len(an.ChangedSymbols), strings.Join(files, ", "), extra)
				}
			} else {
				treeLine = "tree: unavailable (git diff failed)"
			}
		}
	}
	lines = append(lines, treeLine)

	// Last 3 checkpoint receipts — the same one-liners the checkpoint
	// responses minted, newest-first.
	receipts := []string{}
	for _, e := range entries {
		if len(receipts) >= 3 {
			break
		}
		label := e.Claim
		if label == "" {
			label = e.Decision
		}
		receipts = append(receipts, fmt.Sprintf("%s#%d: %s", name, e.Seq, truncLoopText(label, 80)))
	}
	if len(receipts) > 0 {
		lines = append(lines, "recent: "+strings.Join(receipts, " | "))
	}

	// Suggested re-entry seeds: top 3 symbol-ID-shaped tokens from the
	// most recent checkpoints' evidence, omitted entirely when none
	// parse (best-effort — never a guessed ID line).
	evid := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Evidence != "" {
			evid = append(evid, e.Evidence)
		}
	}
	if seeds := parseLoopSeedIDs(evid, 3); len(seeds) > 0 {
		lines = append(lines, "seeds: "+strings.Join(seeds, ", "))
	}

	text := strings.Join(lines, "\n")
	// Hard cap: the manifest must stay a pointer table. The section
	// caps above make overflow rare; a pathological ledger (fat
	// verbatim AWAITING HUMAN entries) still converges here.
	for db.ApproxTokens(text) > loopHandoffDecisionBudget && len(text) > 32 {
		r := []rune(text)
		text = strings.TrimRight(string(r[:len(r)*9/10]), " \n|") + " ...(trimmed)"
	}
	return text
}

// loopHandoff implements `loop action=handoff`: compose the pointer
// manifest server-side and append it as a checkpoint. Response =
// {receipt, manifest} so the writer sees what the next session will —
// `resume` surfaces the handoff at the head of the brief.
func (s *Server) loopHandoff(ctx context.Context, start time.Time, tool string, args map[string]any, projectID, name string) *mcp.CallToolResult {
	if name == "" {
		return s.errResultRich("loop handoff requires `name`", []map[string]string{
			{"tool": "loop", "args": `{"action":"list"}`,
				"why": "see which loops exist before handing one off"},
			{"tool": "loop", "args": `{"action":"handoff","name":"<loop-name>","note":"one line for the next session"}`,
				"why": "compose + append the pointer manifest for a named loop"},
		})
	}
	note := str(args, "note")
	if len([]rune(note)) > loopHandoffNoteMax {
		return s.errResultRich(
			fmt.Sprintf("loop handoff: note is %d chars (max %d) — keep it pointer-shaped; the manifest carries the state and `loop export` renders prose on demand", len([]rune(note)), loopHandoffNoteMax),
			[]map[string]string{
				{"tool": "loop", "args": fmt.Sprintf(`{"action":"handoff","name":%q,"note":"<one line of intent>"}`, name),
					"why": "retry with a short note; details belong in checkpoints, not the handoff"},
			})
	}
	entries, err := s.store.ListLoopCheckpoints(projectID, name, loopHandoffWindow)
	if err != nil {
		return errResult(fmt.Sprintf("loop handoff: %v", err))
	}
	if len(entries) == 0 {
		return s.errResultRich(
			fmt.Sprintf("loop %q has no checkpoints in project %q — a handoff is a pointer manifest over recorded work; record at least one checkpoint first", name, projectID),
			[]map[string]string{
				{"tool": "loop", "args": fmt.Sprintf(`{"action":"start","name":%q,"claim":"<what this loop ships>"}`, name),
					"why": "open the loop; handoff only has meaning once the ledger holds state"},
				{"tool": "loop", "args": `{"action":"list"}`,
					"why": "see which loops already carry checkpoints"},
			})
	}

	claim := loopHandoffClaimPrefix
	if note != "" {
		claim += " — " + note
	}
	manifest := s.composeLoopHandoffManifest(ctx, projectID, name, entries)
	cp := db.LoopCheckpoint{
		ProjectID: projectID,
		LoopName:  name,
		Claim:     claim,
		Decision:  manifest,
		Watermark: s.loopWatermark(),
	}
	seq, err := s.store.AppendLoopCheckpoint(cp)
	if err != nil {
		return errResult(fmt.Sprintf("loop handoff: %v", err))
	}
	data := map[string]any{
		"loop":      name,
		"seq":       seq,
		"watermark": cp.Watermark,
		"receipt":   fmt.Sprintf("%s#%d stored: %s — next session recovers via loop resume (~150 tokens)", name, seq, truncLoopText(claim, 80)),
		"manifest":  manifest,
		"_meta": map[string]any{
			"next_steps": []map[string]string{
				{"tool": "loop", "args": fmt.Sprintf(`{"action":"resume","name":%q}`, name),
					"why": "what the next session sees — the handoff manifest leads the brief"},
				{"tool": "loop", "args": fmt.Sprintf(`{"action":"export","name":%q}`, name),
					"why": "render a human-readable Markdown handoff document on demand"},
			},
		},
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0)
}

// loopExport implements `loop action=export`: render a HUMAN-readable
// Markdown document FROM the ledger, on demand — the "prose only when
// someone actually needs it" escape hatch. Never writes files.
//
// Window selection: `seq` names the checkpoint the document ends at
// (default: the latest handoff, falling back to the newest checkpoint
// when the loop has none). The document starts after the previous
// handoff — one handoff's worth of work — or at the beginning when no
// earlier handoff exists.
func (s *Server) loopExport(start time.Time, tool string, args map[string]any, projectID, name string) *mcp.CallToolResult {
	if name == "" {
		return s.errResultRich("loop export requires `name`", []map[string]string{
			{"tool": "loop", "args": `{"action":"list"}`,
				"why": "see which loops exist and pick one to export"},
		})
	}
	entries, err := s.store.ListLoopCheckpoints(projectID, name, loopHandoffWindow)
	if err != nil {
		return errResult(fmt.Sprintf("loop export: %v", err))
	}
	if len(entries) == 0 {
		return s.errResultRich(
			fmt.Sprintf("loop %q has no checkpoints in project %q — nothing to export", name, projectID),
			[]map[string]string{
				{"tool": "loop", "args": `{"action":"list"}`,
					"why": "see which loops exist and how recently each was touched"},
			})
	}
	// Ascending for document order.
	asc := make([]db.LoopCheckpoint, len(entries))
	for i, e := range entries {
		asc[len(entries)-1-i] = e
	}

	endSeq := intArg(args, "seq", 0)
	if endSeq == 0 {
		for i := len(asc) - 1; i >= 0; i-- {
			if isLoopHandoff(asc[i]) {
				endSeq = asc[i].Seq
				break
			}
		}
		if endSeq == 0 {
			endSeq = asc[len(asc)-1].Seq
		}
	} else {
		found := false
		for _, e := range asc {
			if e.Seq == endSeq {
				found = true
				break
			}
		}
		if !found {
			return s.errResultRich(
				fmt.Sprintf("loop export: seq %d not found in loop %q (window: last %d checkpoints)", endSeq, name, loopHandoffWindow),
				[]map[string]string{
					{"tool": "loop", "args": fmt.Sprintf(`{"action":"list","name":%q}`, name),
						"why": "see the loop's checkpoints and their seq numbers"},
				})
		}
	}
	// Start after the previous handoff strictly before endSeq.
	startAfter := 0
	for _, e := range asc {
		if isLoopHandoff(e) && e.Seq < endSeq {
			startAfter = e.Seq // ascending scan — last match wins
		}
	}
	window := []db.LoopCheckpoint{}
	for _, e := range asc {
		if e.Seq > startAfter && e.Seq <= endSeq {
			window = append(window, e)
		}
	}

	budget := maxTokensArg(args)
	if budget <= 0 {
		budget = loopExportDefaultBudget
	}
	// Newest-first inclusion under the budget (reserving headroom for
	// the trigger sections), rendered oldest-first — same trim shape
	// as resume so the freshest work always survives.
	bodyBudget := budget - 150
	if bodyBudget < 100 {
		bodyBudget = 100
	}
	included := []db.LoopCheckpoint{}
	used := 0
	for i := len(window) - 1; i >= 0; i-- {
		e := window[i]
		cost := db.ApproxTokens(e.Claim+e.Decision+e.Evidence+e.ReopenTrigger+e.Confidence) + 30
		if used+cost > bodyBudget && len(included) > 0 {
			break
		}
		included = append([]db.LoopCheckpoint{e}, included...)
		used += cost
	}
	omitted := len(window) - len(included)

	var b strings.Builder
	fmt.Fprintf(&b, "# Handoff: %s — checkpoints #%d–#%d\n", name, startAfter+1, endSeq)
	if omitted > 0 {
		fmt.Fprintf(&b, "\n_%d earlier iteration(s) omitted (max_tokens=%d) — pass a larger max_tokens to widen._\n", omitted, budget)
	}
	b.WriteString("\n## Iterations\n")
	for _, e := range included {
		claim := e.Claim
		if claim == "" {
			claim = "(no claim)"
		}
		fmt.Fprintf(&b, "\n### %s#%d — %s\n", name, e.Seq, claim)
		if e.Decision != "" {
			fmt.Fprintf(&b, "- decision: %s\n", e.Decision)
		}
		if e.Confidence != "" {
			fmt.Fprintf(&b, "- confidence: %s\n", e.Confidence)
		}
		if e.Evidence != "" {
			fmt.Fprintf(&b, "- evidence: %s\n", e.Evidence)
		}
		if e.ReopenTrigger != "" {
			fmt.Fprintf(&b, "- reopen_trigger: %s\n", e.ReopenTrigger)
		}
	}
	// Trigger sections cover the WHOLE window (not just included) —
	// these are the open ends a human reader most needs.
	open, awaiting := []string{}, []string{}
	for _, e := range window {
		t := strings.TrimSpace(e.ReopenTrigger)
		if t == "" {
			continue
		}
		line := fmt.Sprintf("- #%d: %s", e.Seq, t)
		if strings.Contains(t, loopAwaitingHumanMarker) {
			awaiting = append(awaiting, line)
		} else {
			open = append(open, line)
		}
	}
	b.WriteString("\n## Open reopen triggers\n")
	if len(open) == 0 {
		b.WriteString("_(none)_\n")
	} else {
		b.WriteString(strings.Join(open, "\n") + "\n")
	}
	b.WriteString("\n## Awaiting human\n")
	if len(awaiting) == 0 {
		b.WriteString("_(none)_\n")
	} else {
		b.WriteString(strings.Join(awaiting, "\n") + "\n")
	}

	data := map[string]any{
		"loop":                name,
		"from_seq":            startAfter + 1,
		"to_seq":              endSeq,
		"omitted_checkpoints": omitted,
		"markdown":            b.String(),
	}
	return s.jsonResultWithMeta(data, start, tool, args, 0)
}

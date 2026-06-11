// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/server"
)

// runStatsCLI implements `pincher stats [--json] [--reset] [--data-dir DIR]`.
//
// Without flags: prints a human-readable summary of the persisted savings
// across every recorded session, plus a per-project file/symbol/edge
// breakdown. Sources:
//   - GetAllTimeSavings — sum across the sessions table
//   - ListProjects — per-project counts
//
// `--json` emits the same data as a structured object suitable for
// dashboards / shell pipelines (`pincher stats --json | jq ...`).
//
// `--reset` wipes the sessions table — the pre-existing path was direct
// SQLite via the user's own `sqlite3` invocation, which is brittle and
// requires knowing the schema. Returns the count of rows deleted.
//
// CLI-only (no MCP exposure) by deliberate choice: stats wipe is a
// destructive admin action. An LLM agent should never wipe stats without
// explicit human invocation.
func runStatsCLI(args []string) {
	log.SetOutput(io.Discard)

	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "Override data directory")
	asJSON := fs.Bool("json", false, "Emit structured JSON instead of human-readable text")
	reset := fs.Bool("reset", false, "Wipe the sessions table (clears all-time totals); symbol / edge / project data is unaffected")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pincher stats [--json] [--reset] [--data-dir DIR]")
		fmt.Fprintln(os.Stderr, "  Prints persisted session savings (tokens saved, call count)")
		fmt.Fprintln(os.Stderr, "  plus per-project file/symbol/edge counts. --reset wipes session history")
		fmt.Fprintln(os.Stderr, "  without touching symbol data; back up first via `--json > file.json`.")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	openStore := openStoreReadOnlyOrCreate
	if *reset {
		openStore = openProjectStore
	}
	store, dir, err := openStore(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if *reset {
		runStatsReset(store, dir, *asJSON)
		return
	}

	report, err := buildStatsReport(store, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: stats failed: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return
	}
	fmt.Print(formatStatsText(report))
}

// runStatsReset performs the wipe and emits a one-line confirmation
// (text mode) or a structured object (JSON mode).
func runStatsReset(store *db.Store, dir string, asJSON bool) {
	rows, err := store.ResetSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: stats reset failed: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		out := map[string]any{
			"reset":        true,
			"rows_deleted": rows,
			"data_dir":     dir,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}
	fmt.Printf("Wiped %d session row(s) from %s\n", rows, filepath.Join(dir, "pincher.db"))
}

// StatsReport is the structured form `pincher stats --json` emits.
type StatsReport struct {
	DataDir  string         `json:"data_dir"`
	DBSizeKB int64          `json:"db_size_kb"`
	AllTime  AllTimeSavings `json:"all_time"`
	// Hook7d (hook-redirect-v2) is the trailing-7d PreToolUse hook
	// savings block — the honest "savings vs realistic baseline"
	// number: estimated tokens of the suggested context-lite calls vs
	// the stat-ed size of what the intercepted Reads would actually
	// have returned. Nil (omitted) when no redirect carries the v41
	// telemetry yet, so pre-upgrade output is byte-identical.
	Hook7d   *HookSavingsReport `json:"hook_7d,omitempty"`
	Projects []ProjectStats     `json:"projects"`
}

// HookSavingsReport backs the HOOK 7D section of `pincher stats`.
type HookSavingsReport struct {
	Redirects       int     `json:"redirects"`
	EstTokensServed int64   `json:"est_tokens_served"`
	BaselineTokens  int64   `json:"baseline_tokens"`
	EstSavedPct     float64 `json:"est_saved_pct"`
}

// AllTimeSavings is the sum across every persisted session row.
type AllTimeSavings struct {
	Calls           int64                          `json:"calls"`
	TokensUsed      int64                          `json:"tokens_used"`
	TokensSaved     int64                          `json:"tokens_saved"`
	BaselineMethods map[string]BaselineMethodStats `json:"baseline_methods"`
	// CallsByLanguage is the per-language call tally summed across every
	// recorded session that carried the v16 calls_by_language column
	// (#240). Surfaces "is the agent calling pincher on the file types it
	// works with most" as a one-line check. Empty map when no session
	// has recorded language data yet.
	CallsByLanguage map[string]int64 `json:"calls_by_language,omitempty"`
	// QueryMetrics carries cumulative query-failure / retry counters
	// (#241). Always present on v17+ binaries; pre-v17 sessions
	// contribute zero on every field. Surfaces in `pincher stats` as a
	// RETRIES section that's only rendered when QueriesTotal > 0 — keeps
	// the output noise-free on healthy projects.
	QueryMetrics QueryMetricsReport `json:"query_metrics"`
}

type BaselineMethodStats struct {
	Calls       int64 `json:"calls"`
	TokensUsed  int64 `json:"tokens_used"`
	TokensSaved int64 `json:"tokens_saved"`
}

// QueryMetricsReport mirrors db.QueryMetrics with JSON tags suited for
// the `pincher stats --json` shape. Surfaces the four counters plus
// two derived rates so consumers don't have to recompute them.
//
// Naming honesty note (#1494): the pre-v0.78.1 field `retry_rate` was
// mis-named — it actually held `queries_zero_result / queries_total`,
// which is the rate at which a query returned zero rows, NOT the rate
// at which the caller had to retry. On a healthy codebase the
// dominant zero-result class is audit-shape queries
// (`MATCH (f:Function) WHERE NOT EXISTS { ... }` — zero rows = the
// invariant holds) so the value routinely looked like a five-alarm
// fire while nothing was actually wrong. The honest naming is
// `zero_result_rate`. `retry_success_rate` is the new positive
// signal — when a caller DID hit zero and DID retry, how often did
// the retry succeed.
//
// Half 2 (this PR) ships the naming fix. Half 1 — splitting
// `queries_zero_result` into `queries_zero_expected` (audit-shape) +
// `queries_zero_unexpected` (caller surprised) — needs a schema
// migration and is tracked separately (out of scope for the v0.78.1
// patch line; queued for v0.80 or v0.79 hardening per re-triage).
type QueryMetricsReport struct {
	QueriesTotal            int64 `json:"queries_total"`
	QueriesZeroResult       int64 `json:"queries_zero_result"`
	QueriesRetriedSucceeded int64 `json:"queries_retried_succeeded"`
	TokensBurnedOnFailures  int64 `json:"tokens_burned_on_failures"`
	// ZeroResultRate = queries_zero_result / queries_total. 0 when no
	// queries. Same value as the pre-v0.78.1 `retry_rate` field but
	// under the honest name.
	ZeroResultRate float64 `json:"zero_result_rate"`
	// RetrySuccessRate = queries_retried_succeeded / queries_zero_result.
	// 0 when no zero-result queries (denominator is undefined; surface
	// as 0 not omitted so dashboards have a stable shape). Reads as
	// "of the zero-result calls, what fraction recovered via a retry."
	// A high value here is GOOD (the agent self-corrected); a low
	// value at high zero_result_rate is the actual friction signal.
	RetrySuccessRate float64 `json:"retry_success_rate"`
	// QueriesZeroExpected / QueriesZeroUnexpected (v34, #1632 v0.85)
	// split queries_zero_result into audit-shape (pinchQL with a
	// property predicate, empty rows = healthy codebase) and
	// caller-surprised (search / trace / neighborhood / property-less
	// query, empty rows = friction event). Pre-v34 rows hold zero on
	// both fields; new rows satisfy
	//     queries_zero_expected + queries_zero_unexpected == queries_zero_result.
	QueriesZeroExpected   int64 `json:"queries_zero_expected"`
	QueriesZeroUnexpected int64 `json:"queries_zero_unexpected"`
	// ZeroUnexpectedRate = queries_zero_unexpected / queries_total.
	// The actionable metric — high values flag the
	// "agent calls pincher, gets nothing, falls back to Read/Grep" loop
	// that ZeroResultRate alone couldn't separate from audit-shape
	// healthy zeros.
	ZeroUnexpectedRate float64 `json:"zero_unexpected_rate"`
}

// ProjectStats is a per-project file/symbol/edge breakdown.
type ProjectStats struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Files   int    `json:"files"`
	Symbols int    `json:"symbols"`
	Edges   int    `json:"edges"`
	// SchemaVersionAtIndex is the schema version current when this
	// project was last indexed (#236). Pointer-to-int so we can
	// distinguish nil ("indexed before the column existed — pre-v15")
	// from a recorded value. Omitted from JSON when nil.
	SchemaVersionAtIndex *int `json:"schema_version_at_index,omitempty"`
	// Stale is true when the project's schema version is below the
	// current binary's max-known schema version (or unknown / pre-v15).
	// Computed at render time, not persisted. JSON consumers can use
	// this directly; the CLI table appends a `[stale]` marker.
	Stale bool `json:"stale"`
	// StaleReason carries a short human-readable hint surfaced in
	// `pincher doctor` ("indexed at v12, current is v15"). Empty when
	// Stale is false.
	StaleReason string `json:"stale_reason,omitempty"`
}

func buildStatsReport(store *db.Store, dir string) (*StatsReport, error) {
	calls, tokensUsed, tokensSaved, _, err := store.GetAllTimeSavings()
	if err != nil {
		return nil, fmt.Errorf("all-time savings: %w", err)
	}

	// callsByLanguage is best-effort: a query failure here shouldn't
	// fail the whole stats report (the diagnostic is supplementary,
	// not load-bearing for the cost/tokens summary).
	callsByLang, err := store.GetAllTimeCallsByLanguage()
	if err != nil {
		callsByLang = nil
	}

	// queryMetrics is also best-effort: a stale binary running against
	// a pre-v17 DB will get a zero-value struct here (the COALESCE in
	// the SQL handles missing rows; only a transient query failure
	// would error). Don't fail the whole report on a diagnostic.
	qm, err := store.GetAllTimeQueryMetrics()
	if err != nil {
		qm = db.QueryMetrics{}
	}
	baselineMethods, err := allTimeBaselineMethods(store, calls, tokensUsed, tokensSaved)
	if err != nil {
		baselineMethods = map[string]BaselineMethodStats{}
	}

	// Hook savings (hook-redirect-v2) is best-effort like the other
	// diagnostics: only rendered when at least one redirect carries the
	// v41 baseline telemetry, so the section never shows a meaningless
	// all-zero box on fresh or pre-upgrade installs.
	var hook7d *HookSavingsReport
	if estServed, baselineTok, err := store.HookSavings7d(); err == nil && baselineTok > 0 {
		_, redirects, _, convErr := store.HookConversionRate7d()
		if convErr != nil {
			redirects = 0
		}
		hook7d = &HookSavingsReport{
			Redirects:       redirects,
			EstTokensServed: estServed,
			BaselineTokens:  baselineTok,
			EstSavedPct:     float64(baselineTok-estServed) / float64(baselineTok) * 100,
		}
	}

	projects, err := store.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	currentSchema := db.CurrentSchemaVersion()
	projOut := make([]ProjectStats, 0, len(projects))
	for _, p := range projects {
		stats := ProjectStats{
			ID:                   p.ID,
			Name:                 p.Name,
			Path:                 p.Path,
			Files:                p.FileCount,
			Symbols:              p.SymCount,
			Edges:                p.EdgeCount,
			SchemaVersionAtIndex: p.SchemaVersionAtIndex,
		}
		stats.Stale, stats.StaleReason = staleness(p.SchemaVersionAtIndex, currentSchema)
		projOut = append(projOut, stats)
	}

	var zeroResultRate float64
	if qm.QueriesTotal > 0 {
		zeroResultRate = float64(qm.QueriesZeroResult) / float64(qm.QueriesTotal)
	}
	var retrySuccessRate float64
	if qm.QueriesZeroResult > 0 {
		retrySuccessRate = float64(qm.QueriesRetriedSucceeded) / float64(qm.QueriesZeroResult)
	}
	var zeroUnexpectedRate float64
	if qm.QueriesTotal > 0 {
		zeroUnexpectedRate = float64(qm.QueriesZeroUnexpected) / float64(qm.QueriesTotal)
	}

	return &StatsReport{
		DataDir:  dir,
		DBSizeKB: dbFileSizeKB(dir),
		AllTime: AllTimeSavings{
			Calls:           calls,
			TokensUsed:      tokensUsed,
			TokensSaved:     tokensSaved,
			BaselineMethods: baselineMethods,
			CallsByLanguage: callsByLang,
			QueryMetrics: QueryMetricsReport{
				QueriesTotal:            qm.QueriesTotal,
				QueriesZeroResult:       qm.QueriesZeroResult,
				QueriesRetriedSucceeded: qm.QueriesRetriedSucceeded,
				TokensBurnedOnFailures:  qm.TokensBurnedOnFailures,
				ZeroResultRate:          zeroResultRate,
				RetrySuccessRate:        retrySuccessRate,
				QueriesZeroExpected:     qm.QueriesZeroExpected,
				QueriesZeroUnexpected:   qm.QueriesZeroUnexpected,
				ZeroUnexpectedRate:      zeroUnexpectedRate,
			},
		},
		Hook7d:   hook7d,
		Projects: projOut,
	}, nil
}

func allTimeBaselineMethods(store *db.Store, totalCalls, totalTokensUsed, totalTokensSaved int64) (map[string]BaselineMethodStats, error) {
	rows, err := store.AllTimeToolCallStatsByTool()
	if err != nil {
		return nil, err
	}
	out := map[string]BaselineMethodStats{}
	var attributed BaselineMethodStats
	for _, row := range rows {
		method := server.BaselineMethodForTool(row.Tool)
		stats := out[method]
		stats.Calls += row.CallCount
		stats.TokensUsed += int64(math.Round(row.AvgTokensUsed * float64(row.CallCount)))
		stats.TokensSaved += row.SumTokensSaved
		out[method] = stats
		attributed.Calls += row.CallCount
		attributed.TokensUsed += int64(math.Round(row.AvgTokensUsed * float64(row.CallCount)))
		attributed.TokensSaved += row.SumTokensSaved
	}
	legacy := BaselineMethodStats{
		Calls:       totalCalls - attributed.Calls,
		TokensUsed:  totalTokensUsed - attributed.TokensUsed,
		TokensSaved: totalTokensSaved - attributed.TokensSaved,
	}
	if legacy.Calls > 0 || legacy.TokensUsed > 0 || legacy.TokensSaved > 0 {
		if legacy.Calls < 0 {
			legacy.Calls = 0
		}
		if legacy.TokensUsed < 0 {
			legacy.TokensUsed = 0
		}
		if legacy.TokensSaved < 0 {
			legacy.TokensSaved = 0
		}
		out["legacy_unattributed"] = legacy
	}
	return out, nil
}

// dbFileSizeKB returns the size of pincher.db in KB, or 0 if stat fails.
func dbFileSizeKB(dir string) int64 {
	info, err := os.Stat(filepath.Join(dir, "pincher.db"))
	if err != nil {
		return 0
	}
	return info.Size() / 1024
}

// formatStatsText renders the report as a human-readable text block
// modeled after the MCP `stats` tool's box but without the "live SESSION"
// section since the CLI binary has no in-memory atomic counters.
func formatStatsText(r *StatsReport) string {
	var b strings.Builder

	commify := func(n int64) string {
		s := fmt.Sprintf("%d", n)
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		return s
	}

	// Pre-compute every (label, value) pair so we can size the box to the
	// widest content. Without this, large symbol counts (e.g. thinksmart's
	// 447,201 syms / 39,276 files = 28-char value) overflow the previous
	// 44-char fixed-width box and push the closing │ visually rightward.
	//
	// Section boundaries are tracked as indices rather than hardcoded so a
	// future section insert (#240 added LANGUAGES between STORAGE and
	// PROJECTS) only needs new boundary tracking, not rewiring of every
	// downstream loop.
	type row struct{ label, value string }
	var rows []row

	rows = append(rows,
		row{"Tool calls:", commify(r.AllTime.Calls)},
		row{"Tokens used:", commify(r.AllTime.TokensUsed)},
		row{"Tokens saved:", "~" + commify(r.AllTime.TokensSaved)},
	)
	allTimeEnd := len(rows)

	if len(r.AllTime.BaselineMethods) > 0 {
		methods := make([]string, 0, len(r.AllTime.BaselineMethods))
		for method := range r.AllTime.BaselineMethods {
			methods = append(methods, method)
		}
		sort.Slice(methods, func(i, j int) bool {
			a := r.AllTime.BaselineMethods[methods[i]]
			b := r.AllTime.BaselineMethods[methods[j]]
			if a.Calls != b.Calls {
				return a.Calls > b.Calls
			}
			return methods[i] < methods[j]
		})
		for _, method := range methods {
			stats := r.AllTime.BaselineMethods[method]
			rows = append(rows, row{
				method + ":",
				fmt.Sprintf("%s calls / ~%s saved", commify(stats.Calls), commify(stats.TokensSaved)),
			})
		}
	}
	baselinesEnd := len(rows)

	rows = append(rows,
		row{"Data dir:", r.DataDir}, // no truncation; box auto-sizes
		row{"DB size:", commify(r.DBSizeKB) + " KB"},
	)
	storageEnd := len(rows)

	// LANGUAGES rows (#240): one per language, sorted by count desc with a
	// lexical tie-breaker so the diagnostic order is stable across runs.
	// Only emitted when the underlying map is non-empty — pre-v16
	// sessions render exactly the same as before.
	if len(r.AllTime.CallsByLanguage) > 0 {
		type langKV struct {
			lang  string
			count int64
		}
		pairs := make([]langKV, 0, len(r.AllTime.CallsByLanguage))
		for lang, count := range r.AllTime.CallsByLanguage {
			pairs = append(pairs, langKV{lang, count})
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].count != pairs[j].count {
				return pairs[i].count > pairs[j].count
			}
			return pairs[i].lang < pairs[j].lang
		})
		for _, p := range pairs {
			rows = append(rows, row{p.lang + ":", commify(p.count)})
		}
	}
	languagesEnd := len(rows)

	// RETRIES rows (#241): query-failure / retry-rate counters surfaced
	// only when at least one query-shaped call has been recorded. On a
	// healthy project with low zero-result rate the whole section is
	// omitted to avoid noise; on a misaligned-default project the high
	// zero-result row jumps out as a "your threshold is wrong" signal.
	//
	// #1494: "Retry rate:" label was mis-named — the value is actually
	// queries_zero_result / queries_total, which on a healthy
	// codebase is dominated by audit-shape queries (intentional zero
	// rows). The honest label is "Zero-result rate:". A new "Retry
	// success:" row surfaces queries_retried_succeeded /
	// queries_zero_result so users can see how often the agent
	// recovered after a zero result; only rendered when there ARE
	// zero results to avoid a divide-by-zero-ish display on perfectly
	// healthy projects.
	if r.AllTime.QueryMetrics.QueriesTotal > 0 {
		qm := r.AllTime.QueryMetrics
		rows = append(rows,
			row{"Total queries:", commify(qm.QueriesTotal)},
			row{"Zero-result:", commify(qm.QueriesZeroResult)},
			row{"Zero-result rate:", fmt.Sprintf("%.1f%%", qm.ZeroResultRate*100)},
		)
		// #1632 v0.85: when the split sub-counters carry any signal
		// (post-v34 sessions have at least one row), surface the
		// audit-shape vs unexpected breakdown. Pre-v34 sessions hold
		// zero on both counters; rendering them as 0.0% is honest but
		// noisy — gate on whether either sub-counter is non-zero so
		// pure-pre-v34 stats render exactly as before.
		if qm.QueriesZeroExpected+qm.QueriesZeroUnexpected > 0 {
			rows = append(rows,
				row{"  Audit-shape:", commify(qm.QueriesZeroExpected)},
				row{"  Unexpected:", commify(qm.QueriesZeroUnexpected)},
				row{"Zero-unexpected rate:", fmt.Sprintf("%.1f%%", qm.ZeroUnexpectedRate*100)},
			)
		}
		rows = append(rows, row{"Recovered:", commify(qm.QueriesRetriedSucceeded)})
		if qm.QueriesZeroResult > 0 {
			rows = append(rows, row{"Retry success:", fmt.Sprintf("%.1f%%", qm.RetrySuccessRate*100)})
		}
		rows = append(rows, row{"Tokens burned:", commify(qm.TokensBurnedOnFailures)})
	}
	retriesEnd := len(rows)

	// HOOK 7D rows (hook-redirect-v2): only emitted when redirect rows
	// carry the v41 savings telemetry — fresh / pre-upgrade installs
	// render exactly as before.
	if r.Hook7d != nil {
		rows = append(rows,
			row{"Redirects (7d):", commify(int64(r.Hook7d.Redirects))},
			row{"Est. served:", "~" + commify(r.Hook7d.EstTokensServed)},
			row{"Read baseline:", "~" + commify(r.Hook7d.BaselineTokens)},
			row{"Est. saved:", fmt.Sprintf("%.1f%%", r.Hook7d.EstSavedPct)},
		)
	}
	hookEnd := len(rows)

	for _, p := range r.Projects {
		// Two-line project rendering: name on line 1, count value on line 2.
		// Both contribute to width independently.
		name := p.Name
		if p.Stale {
			name += " [stale]"
		}
		rows = append(rows,
			row{name + ":", ""},
			row{"", fmt.Sprintf("%s syms / %s files",
				commify(int64(p.Symbols)),
				commify(int64(p.Files)))})
	}

	// content layout: "  %-20s %s" — 2 leading spaces + label (padded to 20
	// when shorter, expanded when longer because Go's %-Ns format is min-width
	// not truncating) + 1 space + value. Inner width must accommodate the
	// widest such content, including label-overflow rows like project names
	// used as labels with empty values.
	const labelWidth = 20
	const minBoxWidth = 44
	const maxBoxWidth = 100 // cap so terminals don't wrap absurdly on outliers
	effLabelLen := func(label string) int {
		if len(label) > labelWidth {
			return len(label)
		}
		return labelWidth
	}
	w := minBoxWidth
	for _, r := range rows {
		need := 2 + effLabelLen(r.label) + 1 + len(r.value)
		if need > w {
			w = need
		}
	}
	if w > maxBoxWidth {
		w = maxBoxWidth
		// At the cap, truncate to keep the closing │ aligned. Value gets
		// truncated first (preferred — labels usually carry the meaning).
		// If label alone exceeds the cap, label gets truncated too.
		for i := range rows {
			label := rows[i].label
			value := rows[i].value
			availForValue := w - 2 - effLabelLen(label) - 1
			if availForValue < 0 {
				availForLabel := w - 2 - 1
				if availForLabel < 1 {
					availForLabel = 1
				}
				label = truncEnd(label, availForLabel)
				availForValue = 0
			}
			if len(value) > availForValue {
				value = truncEnd(value, availForValue)
			}
			rows[i].label = label
			rows[i].value = value
		}
	}

	header := func(title string) string {
		pad := w - 2 - len(title)
		left := pad / 2
		right := pad - left
		return "│ " + strings.Repeat(" ", left) + title + strings.Repeat(" ", right) + " │\n"
	}
	line := func(label, value string) string {
		content := fmt.Sprintf("  %-20s %s", label, value)
		if len(content) < w {
			content += strings.Repeat(" ", w-len(content))
		}
		return "│" + content + "│\n"
	}
	sep := "├" + strings.Repeat("─", w) + "┤\n"

	b.WriteString("┌" + strings.Repeat("─", w) + "┐\n")
	b.WriteString(header("ALL-TIME"))
	for i := 0; i < allTimeEnd; i++ {
		b.WriteString(line(rows[i].label, rows[i].value))
	}

	if allTimeEnd < baselinesEnd {
		b.WriteString(sep)
		b.WriteString(header("BASELINES"))
		for i := allTimeEnd; i < baselinesEnd; i++ {
			b.WriteString(line(rows[i].label, rows[i].value))
		}
	}

	b.WriteString(sep)
	b.WriteString(header("STORAGE"))
	for i := baselinesEnd; i < storageEnd; i++ {
		b.WriteString(line(rows[i].label, rows[i].value))
	}

	if storageEnd < languagesEnd {
		b.WriteString(sep)
		b.WriteString(header("LANGUAGES"))
		for i := storageEnd; i < languagesEnd; i++ {
			b.WriteString(line(rows[i].label, rows[i].value))
		}
	}

	if languagesEnd < retriesEnd {
		b.WriteString(sep)
		b.WriteString(header("RETRIES"))
		for i := languagesEnd; i < retriesEnd; i++ {
			b.WriteString(line(rows[i].label, rows[i].value))
		}
	}

	if retriesEnd < hookEnd {
		b.WriteString(sep)
		b.WriteString(header("HOOK 7D"))
		for i := retriesEnd; i < hookEnd; i++ {
			b.WriteString(line(rows[i].label, rows[i].value))
		}
	}

	if len(r.Projects) > 0 {
		b.WriteString(sep)
		b.WriteString(header("PROJECTS"))
		for i := hookEnd; i < len(rows); i++ {
			b.WriteString(line(rows[i].label, rows[i].value))
		}
	}

	b.WriteString("└" + strings.Repeat("─", w) + "┘\n")
	return b.String()
}

// truncMid and truncEnd live in cmd/pinch/doctor.go (same `main` package);
// they're shared between the doctor and stats subcommands.

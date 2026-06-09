// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/server"
)

// runSavingsCLI implements `pincher savings report` — a falsifiable _meta ROI
// report built from persisted per-tool call rows.
func runSavingsCLI(args []string) {
	log.SetOutput(io.Discard)
	usage := func() {
		fmt.Fprintln(os.Stderr, "usage: pincher savings report [--since 7d] [--project PATH] [--json] [--data-dir DIR]")
		fmt.Fprintln(os.Stderr, "  Emits raw token inputs, formulas, baseline methods, and schema-gap warnings.")
	}
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help", "help":
			usage()
			os.Exit(0)
		}
	}
	if len(args) == 0 || args[0] != "report" {
		usage()
		os.Exit(2)
	}

	fs := flag.NewFlagSet("savings report", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "Override data directory")
	sinceRaw := fs.String("since", "7d", "Recent-window duration (for example 24h, 7d, 168h)")
	project := fs.String("project", "", "Project path/name to annotate in the report; per-call savings rows are currently not project-scoped")
	asJSON := fs.Bool("json", false, "Emit structured JSON instead of human-readable text")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pincher savings report [--since 7d] [--project PATH] [--json] [--data-dir DIR]")
		fmt.Fprintln(os.Stderr, "  Emits raw token inputs, formulas, baseline methods, and schema-gap warnings.")
		fs.PrintDefaults()
	}
	fs.Parse(args[1:])

	since, err := parseSavingsSince(*sinceRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: invalid --since: %v\n", err)
		os.Exit(2)
	}

	store, dir, err := openStoreReadOnlyOrCreate(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	report, err := buildSavingsReport(store, dir, since, *project, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: savings report failed: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return
	}
	fmt.Print(formatSavingsReportText(report))
}

type SavingsReport struct {
	DataDir       string             `json:"data_dir"`
	GeneratedAt   time.Time          `json:"generated_at"`
	Since         string             `json:"since"`
	ProjectFilter SavingsProjectNote `json:"project_filter"`
	Formula       SavingsFormula     `json:"formula"`
	AllTime       SavingsWindow      `json:"all_time"`
	RecentWindow  SavingsWindow      `json:"recent_window"`
	Warnings      []string           `json:"warnings"`
}

type SavingsProjectNote struct {
	Requested string `json:"requested,omitempty"`
	Applied   bool   `json:"applied"`
	Reason    string `json:"reason,omitempty"`
}

type SavingsFormula struct {
	BaselineTokens string `json:"baseline_tokens"`
	SavingsPct     string `json:"savings_pct"`
	ClaimRule      string `json:"claim_rule"`
}

type SavingsWindow struct {
	Name                  string             `json:"name"`
	Start                 *time.Time         `json:"start,omitempty"`
	End                   *time.Time         `json:"end,omitempty"`
	Calls                 int64              `json:"calls"`
	TokensUsed            int64              `json:"tokens_used"`
	TokensSaved           int64              `json:"tokens_saved"`
	BaselineTokens        int64              `json:"baseline_tokens"`
	SavingsPct            *float64           `json:"savings_pct,omitempty"`
	AggregateClaimAllowed bool               `json:"aggregate_claim_allowed"`
	NoRecentData          bool               `json:"no_recent_data,omitempty"`
	SchemaGaps            SavingsSchemaGaps  `json:"schema_gaps"`
	ByTool                []SavingsToolRow   `json:"by_tool"`
	ByBaselineMethod      []SavingsMethodRow `json:"by_baseline_method"`
}

type SavingsSchemaGaps struct {
	MissingTokensSaved    int64 `json:"missing_tokens_saved"`
	MissingTokensSavedPct int64 `json:"missing_tokens_saved_pct"`
	NoBaselineMethod      int64 `json:"no_baseline_method"`
}

type SavingsToolRow struct {
	Tool                  string     `json:"tool"`
	ComplexityTier        string     `json:"complexity_tier"`
	BaselineMethod        string     `json:"baseline_method"`
	Calls                 int64      `json:"calls"`
	TokensUsed            int64      `json:"tokens_used"`
	TokensSaved           int64      `json:"tokens_saved"`
	BaselineTokens        int64      `json:"baseline_tokens"`
	SavingsPct            *float64   `json:"savings_pct,omitempty"`
	AvgTokensSavedPct     float64    `json:"avg_tokens_saved_pct"`
	MissingTokensSaved    int64      `json:"missing_tokens_saved"`
	MissingTokensSavedPct int64      `json:"missing_tokens_saved_pct"`
	FirstSeen             *time.Time `json:"first_seen,omitempty"`
	LastSeen              *time.Time `json:"last_seen,omitempty"`
}

type SavingsMethodRow struct {
	BaselineMethod string `json:"baseline_method"`
	Calls          int64  `json:"calls"`
	TokensUsed     int64  `json:"tokens_used"`
	TokensSaved    int64  `json:"tokens_saved"`
	BaselineTokens int64  `json:"baseline_tokens"`
}

func buildSavingsReport(store *db.Store, dir string, since time.Duration, project string, now time.Time) (*SavingsReport, error) {
	allRows, err := store.ToolCallSavingsReportRows()
	if err != nil {
		return nil, fmt.Errorf("all-time savings rows: %w", err)
	}
	cutoff := now.Add(-since)
	recentRows, err := store.ToolCallSavingsReportRowsSince(cutoff)
	if err != nil {
		return nil, fmt.Errorf("recent savings rows: %w", err)
	}
	all := buildSavingsWindow("all_time", nil, &now, allRows)
	recent := buildSavingsWindow("recent_window", &cutoff, &now, recentRows)
	if recent.Calls == 0 {
		recent.NoRecentData = true
	}
	note := SavingsProjectNote{Requested: project, Applied: false}
	if project != "" {
		note.Reason = "session_tool_calls rows do not persist project_id yet; project is annotated but not used as a filter"
	}
	report := &SavingsReport{
		DataDir:       dir,
		GeneratedAt:   now,
		Since:         since.String(),
		ProjectFilter: note,
		Formula: SavingsFormula{
			BaselineTokens: "tokens_used + tokens_saved",
			SavingsPct:     "tokens_saved / (tokens_used + tokens_saved) * 100",
			ClaimRule:      "aggregate_claim_allowed is false when any row lacks tokens_saved/tokens_saved_pct or uses baseline_method=none",
		},
		AllTime:      all,
		RecentWindow: recent,
	}
	if !all.AggregateClaimAllowed {
		report.Warnings = append(report.Warnings, "all-time aggregate savings claim refused: baseline fields are incomplete or include baseline_method=none")
	}
	if recent.NoRecentData {
		report.Warnings = append(report.Warnings, "recent-window aggregate savings claim refused: no recent per-call rows in the requested window")
	} else if !recent.AggregateClaimAllowed {
		report.Warnings = append(report.Warnings, "recent-window aggregate savings claim refused: baseline fields are incomplete or include baseline_method=none")
	}
	if project != "" {
		report.Warnings = append(report.Warnings, note.Reason)
	}
	return report, nil
}

func buildSavingsWindow(name string, start, end *time.Time, rows []db.ToolCallSavingsRow) SavingsWindow {
	out := SavingsWindow{Name: name, Start: start, End: end, AggregateClaimAllowed: true, ByTool: []SavingsToolRow{}, ByBaselineMethod: []SavingsMethodRow{}}
	methods := map[string]*SavingsMethodRow{}
	for _, r := range rows {
		method := server.BaselineMethodForTool(r.Tool)
		baseline := r.TokensUsed + r.TokensSaved
		var pct *float64
		if baseline > 0 && r.MissingTokensSaved == 0 {
			v := float64(r.TokensSaved) / float64(baseline) * 100
			pct = &v
		}
		toolRow := SavingsToolRow{
			Tool:                  r.Tool,
			ComplexityTier:        r.ComplexityTier,
			BaselineMethod:        method,
			Calls:                 r.CallCount,
			TokensUsed:            r.TokensUsed,
			TokensSaved:           r.TokensSaved,
			BaselineTokens:        baseline,
			SavingsPct:            pct,
			AvgTokensSavedPct:     r.AvgTokensSavedPct,
			MissingTokensSaved:    r.MissingTokensSaved,
			MissingTokensSavedPct: r.MissingTokensSavedPct,
		}
		if r.FirstSeenUnixNano > 0 {
			v := time.Unix(0, r.FirstSeenUnixNano)
			toolRow.FirstSeen = &v
		}
		if r.LastSeenUnixNano > 0 {
			v := time.Unix(0, r.LastSeenUnixNano)
			toolRow.LastSeen = &v
		}
		out.ByTool = append(out.ByTool, toolRow)
		out.Calls += r.CallCount
		out.TokensUsed += r.TokensUsed
		out.TokensSaved += r.TokensSaved
		out.SchemaGaps.MissingTokensSaved += r.MissingTokensSaved
		out.SchemaGaps.MissingTokensSavedPct += r.MissingTokensSavedPct
		if method == "none" {
			out.SchemaGaps.NoBaselineMethod += r.CallCount
		}
		m := methods[method]
		if m == nil {
			m = &SavingsMethodRow{BaselineMethod: method}
			methods[method] = m
		}
		m.Calls += r.CallCount
		m.TokensUsed += r.TokensUsed
		m.TokensSaved += r.TokensSaved
		m.BaselineTokens += baseline
	}
	out.BaselineTokens = out.TokensUsed + out.TokensSaved
	if out.BaselineTokens > 0 && out.SchemaGaps.MissingTokensSaved == 0 {
		v := float64(out.TokensSaved) / float64(out.BaselineTokens) * 100
		out.SavingsPct = &v
	}
	if out.Calls == 0 || out.SchemaGaps.MissingTokensSaved > 0 || out.SchemaGaps.MissingTokensSavedPct > 0 || out.SchemaGaps.NoBaselineMethod > 0 {
		out.AggregateClaimAllowed = false
	}
	for _, row := range methods {
		out.ByBaselineMethod = append(out.ByBaselineMethod, *row)
	}
	sort.Slice(out.ByBaselineMethod, func(i, j int) bool {
		if out.ByBaselineMethod[i].Calls != out.ByBaselineMethod[j].Calls {
			return out.ByBaselineMethod[i].Calls > out.ByBaselineMethod[j].Calls
		}
		return out.ByBaselineMethod[i].BaselineMethod < out.ByBaselineMethod[j].BaselineMethod
	})
	return out
}

func parseSavingsSince(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(raw, "d") {
		daysRaw := strings.TrimSuffix(raw, "d")
		var days int64
		if _, err := fmt.Sscanf(daysRaw, "%d", &days); err != nil || days <= 0 {
			return 0, fmt.Errorf("%q must be a positive day count like 7d", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return d, nil
}

func formatSavingsReportText(r *SavingsReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pincher savings report — generated %s\n", r.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "formula: baseline_tokens = %s; savings_pct = %s\n\n", r.Formula.BaselineTokens, r.Formula.SavingsPct)
	writeSavingsWindowText(&b, "ALL-TIME", r.AllTime)
	fmt.Fprintln(&b)
	writeSavingsWindowText(&b, "RECENT ("+r.Since+")", r.RecentWindow)
	if len(r.Warnings) > 0 {
		fmt.Fprintln(&b, "\nwarnings:")
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}
	return b.String()
}

func writeSavingsWindowText(b *strings.Builder, title string, w SavingsWindow) {
	fmt.Fprintf(b, "%s\n", title)
	fmt.Fprintf(b, "  calls: %d\n", w.Calls)
	fmt.Fprintf(b, "  tokens_used: %d\n", w.TokensUsed)
	fmt.Fprintf(b, "  tokens_saved: %d\n", w.TokensSaved)
	fmt.Fprintf(b, "  baseline_tokens: %d\n", w.BaselineTokens)
	if w.SavingsPct != nil && w.AggregateClaimAllowed {
		fmt.Fprintf(b, "  savings_pct: %.1f%%\n", *w.SavingsPct)
	} else {
		fmt.Fprintf(b, "  savings_pct: refused (missing baseline fields or no baseline)\n")
	}
	fmt.Fprintf(b, "  schema_gaps: missing_saved=%d missing_pct=%d no_baseline=%d\n", w.SchemaGaps.MissingTokensSaved, w.SchemaGaps.MissingTokensSavedPct, w.SchemaGaps.NoBaselineMethod)
	if len(w.ByBaselineMethod) > 0 {
		fmt.Fprintf(b, "  by_baseline_method:\n")
		for _, m := range w.ByBaselineMethod {
			fmt.Fprintf(b, "    - %s: %d calls, used=%d, saved=%d, baseline=%d\n", m.BaselineMethod, m.Calls, m.TokensUsed, m.TokensSaved, m.BaselineTokens)
		}
	}
}

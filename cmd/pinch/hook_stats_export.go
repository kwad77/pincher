// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// runHookStatsCLI implements `pincher hook-stats --export-7d`.
//
// #662: opt-in user-initiated export of the hook conversion-rate metrics
// that the v0.37 dashboard panel reads from /v1/hook-stats. The intent
// is to give users an easy way to share their numbers when contributing
// to the field-data thread (#640) — the v1.0 README claim of "≥60%
// conversion rate" stays a prototype guess until ≥20 distinct installs
// report measurements.
//
// Telemetry stays local-only per #626. This subcommand reads the same
// data the dashboard already exposes; it does NOT phone home. The user
// runs it, sees the JSON, and chooses whether to paste it on the
// #640 thread.
//
// Anonymization defaults: no project paths, no file paths, no hostnames,
// no install path. Only aggregate counts and tool-level breakdowns —
// the same shape /v1/hook-stats returns plus a small `meta` block so
// the recipient can tell pincher versions apart. `--include-host` is an
// opt-in flag for users who want to include their pincher version /
// OS / arch identifier for outlier triage.
//
// CLI-only by deliberate choice: the data is fundamentally
// human-shareable (paste it on a GitHub thread), not LLM-consumable.
// Adding an MCP surface would tempt an agent to "report telemetry" which
// is exactly the phone-home shape we said we'd never adopt.
func runHookStatsCLI(args []string) {
	log.SetOutput(io.Discard)

	fs := flag.NewFlagSet("hook-stats", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "Override data directory")
	export7d := fs.Bool("export-7d", false, "Emit a shareable JSON snapshot of the trailing 7-day hook conversion-rate metrics")
	includeHost := fs.Bool("include-host", false, "Include pincher version + OS/arch in the export (off by default; enable when contributing to #640 outlier triage)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pincher hook-stats --export-7d [--include-host] [--data-dir DIR]")
		fmt.Fprintln(os.Stderr, "  Emits a shareable JSON snapshot of trailing 7-day PreToolUse hook")
		fmt.Fprintln(os.Stderr, "  conversion-rate metrics. Anonymized by default — no paths or hostnames.")
		fmt.Fprintln(os.Stderr, "  Telemetry stays local; this subcommand reads what the dashboard already shows.")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if !*export7d {
		fs.Usage()
		os.Exit(2)
	}

	store, _, err := openStoreReadOnlyOrCreate(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	report, err := buildHookStatsExport(store, *includeHost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher: hook-stats failed: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "pincher: encode failed: %v\n", err)
		os.Exit(1)
	}
}

// hookStatsExport is the JSON shape emitted by `pincher hook-stats
// --export-7d`. Mirrors the /v1/hook-stats response with two
// additions: a `meta` block carrying schema version + capture
// timestamp, and an optional `host` block (omitted by default).
type hookStatsExport struct {
	Schema      string `json:"schema"`       // bump when the shape changes
	Window      string `json:"window"`       // always "7d" for now
	GeneratedAt string `json:"generated_at"` // RFC3339
	Conversion  struct {
		Pct       float64 `json:"pct"`
		Redirects int     `json:"redirects"`
		Taken     int     `json:"taken"`
	} `json:"conversion"`
	Override struct {
		Pct       float64 `json:"pct"`
		Overrides int     `json:"overrides"`
		Resolved  int     `json:"resolved"`
	} `json:"override"`
	ByTool map[string]map[string]int `json:"by_tool"`
	Host   *hookStatsHost            `json:"host,omitempty"`
}

type hookStatsHost struct {
	PincherVersion string `json:"pincher_version"`
	GoVersion      string `json:"go_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
}

// buildHookStatsExport assembles the export object from the same store
// methods the /v1/hook-stats HTTP handler uses. Kept here rather than
// re-using the handler so CLI execution doesn't require boot of the
// MCP server (slow + side-effecting).
func buildHookStatsExport(store *db.Store, includeHost bool) (*hookStatsExport, error) {
	convPct, redirects, taken, err := store.HookConversionRate7d()
	if err != nil {
		return nil, fmt.Errorf("conversion: %w", err)
	}
	ovrPct, overrides, resolved, err := store.HookOverrideRate7d()
	if err != nil {
		return nil, fmt.Errorf("override: %w", err)
	}
	byTool, err := store.HookCountsByTool7d()
	if err != nil {
		return nil, fmt.Errorf("by_tool: %w", err)
	}
	if byTool == nil {
		byTool = map[string]map[string]int{}
	}

	out := &hookStatsExport{
		Schema:      "pincher.hook-stats.v1",
		Window:      "7d",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ByTool:      byTool,
	}
	out.Conversion.Pct = convPct
	out.Conversion.Redirects = redirects
	out.Conversion.Taken = taken
	out.Override.Pct = ovrPct
	out.Override.Overrides = overrides
	out.Override.Resolved = resolved

	if includeHost {
		out.Host = &hookStatsHost{
			PincherVersion: version, // set by the build's -X main.version
			GoVersion:      runtime.Version(),
			OS:             runtime.GOOS,
			Arch:           runtime.GOARCH,
		}
	}
	return out, nil
}

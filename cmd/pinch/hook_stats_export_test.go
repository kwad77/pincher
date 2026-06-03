// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// Positive: an export against a store with logged hook invocations
// emits the canonical schema with non-zero counts. Mirrors the data
// path used by /v1/hook-stats so the dashboard and the CLI always
// agree on shape.
func TestBuildHookStatsExport_PopulatedStore(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	now := time.Now().UnixNano()
	// Two redirects, one taken (resolved).
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: now, SessionID: "s1", ToolName: "Read",
		FilePath: "a.go", FileBytes: 50000,
		Decision: "redirect", SuggestedTool: "context",
	}); err != nil {
		t.Fatalf("log 1: %v", err)
	}
	if err := store.LogHookInvocation(db.HookInvocation{
		TS: now + 1, SessionID: "s1", ToolName: "Grep",
		Decision: "redirect", SuggestedTool: "search",
	}); err != nil {
		t.Fatalf("log 2: %v", err)
	}
	if _, err := store.ResolveHookInvocationsForSession("s1", []db.HookSessionCall{
		{TS: now + 10, ToolName: "context"},
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	out, err := buildHookStatsExport(store, false)
	if err != nil {
		t.Fatalf("buildHookStatsExport: %v", err)
	}
	if out.Schema != "pincher.hook-stats.v1" {
		t.Errorf("Schema = %q, want pincher.hook-stats.v1", out.Schema)
	}
	if out.Window != "7d" {
		t.Errorf("Window = %q, want 7d", out.Window)
	}
	if out.Conversion.Redirects != 2 {
		t.Errorf("Conversion.Redirects = %d, want 2", out.Conversion.Redirects)
	}
	if out.Conversion.Taken != 1 {
		t.Errorf("Conversion.Taken = %d, want 1", out.Conversion.Taken)
	}
	if out.Host != nil {
		t.Errorf("Host populated despite includeHost=false: %+v", out.Host)
	}
	// generated_at should parse as RFC3339.
	if _, err := time.Parse(time.RFC3339, out.GeneratedAt); err != nil {
		t.Errorf("GeneratedAt %q not RFC3339: %v", out.GeneratedAt, err)
	}
}

// Positive: --include-host populates the host block with runtime
// fields. Verifies the opt-in path used by #640 outlier triage.
func TestBuildHookStatsExport_IncludeHost(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	out, err := buildHookStatsExport(store, true)
	if err != nil {
		t.Fatalf("buildHookStatsExport: %v", err)
	}
	if out.Host == nil {
		t.Fatal("Host is nil despite includeHost=true")
	}
	if out.Host.GoVersion == "" || out.Host.OS == "" || out.Host.Arch == "" {
		t.Errorf("Host fields incomplete: %+v", out.Host)
	}
}

// Negative — empty store: an export against a fresh DB with zero hook
// invocations must still produce a valid object with zero counts (not
// error, not panic). The dashboard's "no data yet" rendering depends
// on this shape so the export must match.
func TestBuildHookStatsExport_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	out, err := buildHookStatsExport(store, false)
	if err != nil {
		t.Fatalf("buildHookStatsExport: %v", err)
	}
	if out.Conversion.Redirects != 0 || out.Conversion.Taken != 0 {
		t.Errorf("empty store has non-zero conversion: %+v", out.Conversion)
	}
	if out.Override.Overrides != 0 || out.Override.Resolved != 0 {
		t.Errorf("empty store has non-zero override: %+v", out.Override)
	}
	if out.ByTool == nil {
		t.Error("ByTool is nil; want non-nil empty map (JSON {} not null)")
	}
}

// Cross-check — JSON round-trip preserves the export shape exactly. A
// recipient of `pincher hook-stats --export-7d > foo.json` must be
// able to decode it back into the same struct without field loss.
// Pins the JSON tags against silent renames.
func TestHookStatsExport_JSONRoundTrip(t *testing.T) {
	src := hookStatsExport{
		Schema:      "pincher.hook-stats.v1",
		Window:      "7d",
		GeneratedAt: "2026-05-17T12:00:00Z",
		ByTool: map[string]map[string]int{
			"Read": {"redirects": 5, "taken": 3},
		},
		Host: &hookStatsHost{
			PincherVersion: "0.71.0",
			GoVersion:      "go1.24",
			OS:             "linux",
			Arch:           "amd64",
		},
	}
	src.Conversion.Pct = 60.0
	src.Conversion.Redirects = 5
	src.Conversion.Taken = 3
	src.Override.Pct = 10.0
	src.Override.Overrides = 1
	src.Override.Resolved = 9

	blob, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Pin the exact field names so a JSON-tag rename is visible.
	for _, want := range []string{
		`"schema":"pincher.hook-stats.v1"`,
		`"window":"7d"`,
		`"generated_at":"2026-05-17T12:00:00Z"`,
		`"conversion":{`, `"pct":60`, `"redirects":5`, `"taken":3`,
		`"override":{`, `"overrides":1`, `"resolved":9`,
		`"by_tool":{`,
		`"host":{`, `"pincher_version":"0.71.0"`, `"go_version":"go1.24"`, `"os":"linux"`, `"arch":"amd64"`,
	} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("missing field %q in:\n%s", want, blob)
		}
	}

	var back hookStatsExport
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Conversion.Redirects != 5 || back.Override.Overrides != 1 {
		t.Errorf("round-trip lost fields: %+v", back)
	}
}

// Negative — `host` field is omitempty: with includeHost=false the
// JSON should not contain `"host"` at all. Recipients of an
// anonymized export rely on the absence to confirm anonymization.
func TestHookStatsExport_HostOmitemptyWhenAnonymized(t *testing.T) {
	out := hookStatsExport{
		Schema: "pincher.hook-stats.v1", Window: "7d",
		ByTool: map[string]map[string]int{},
	}
	blob, _ := json.Marshal(out)
	if strings.Contains(string(blob), `"host"`) {
		t.Errorf("anonymized export contains \"host\" key:\n%s", blob)
	}
}

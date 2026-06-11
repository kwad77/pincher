// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Session-delta _meta: capabilities is a per-server constant, so only
// the FIRST response of a session carries it; subsequent responses
// omit it and stamp `meta_delta: true`. PINCHER_META_DELTA=0 restores
// the legacy every-call emission. Per-tool fields (complexity_tier,
// baseline_method) are never delta'd.

func metaOf(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("nil/empty tool result")
	}
	text := result.Content[0].(*mcp.TextContent).Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	meta, _ := parsed["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("response missing _meta")
	}
	return meta
}

// Positive: first response carries the full capabilities array (no
// meta_delta marker); the second omits capabilities and stamps
// meta_delta=true. Per-tool fields survive on both.
func TestMetaDelta_FirstResponseFull_SecondResponseDelta(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	first := metaOf(t, srv.jsonResultWithMeta(
		map[string]any{"ok": true}, time.Now(), "search", map[string]any{}, 0))
	caps, ok := first["capabilities"].([]any)
	if !ok || len(caps) == 0 {
		t.Fatalf("first response must carry non-empty capabilities; got %v", first["capabilities"])
	}
	if _, present := first["meta_delta"]; present {
		t.Errorf("first response must not carry meta_delta")
	}

	second := metaOf(t, srv.jsonResultWithMeta(
		map[string]any{"ok": true}, time.Now(), "search", map[string]any{}, 0))
	if _, present := second["capabilities"]; present {
		t.Errorf("second response must omit capabilities under session-delta; got %v", second["capabilities"])
	}
	if v, _ := second["meta_delta"].(bool); !v {
		t.Errorf("second response must stamp meta_delta=true so omission reads as intent, not accident; got %v", second["meta_delta"])
	}
	// Per-tool fields are NOT delta'd — they vary call to call.
	for _, k := range []string{"complexity_tier", "baseline_method"} {
		if _, ok := second[k]; !ok {
			t.Errorf("second response must keep per-tool field %q", k)
		}
	}

	// Log the measured per-call saving for the changelog: the omitted
	// capabilities field is exactly what every post-first call stops
	// paying.
	b, _ := json.Marshal(map[string]any{"capabilities": srv.capabilities})
	t.Logf("session-delta saving: ~%d tokens/call (capabilities field, %d bytes)", db.ApproxTokens(string(b)), len(b))
}

// Re-emit on change: when the capabilities slice is recomputed (e.g.
// SetMCPHTTPPath adds streamable_http), the next response carries the
// full new advertisement so consumers never act on a stale set.
func TestMetaDelta_ReemitsWhenCapabilitiesChange(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	// Burn the first emission.
	metaOf(t, srv.jsonResultWithMeta(map[string]any{}, time.Now(), "search", map[string]any{}, 0))

	// Mutate the advertisement (stands in for SetMCPHTTPPath's
	// computeCapabilities re-run).
	srv.capabilities = append(append([]string{}, srv.capabilities...), "test_new_capability")

	third := metaOf(t, srv.jsonResultWithMeta(map[string]any{}, time.Now(), "search", map[string]any{}, 0))
	caps, ok := third["capabilities"].([]any)
	if !ok {
		t.Fatalf("changed capabilities must be re-emitted in full; got %v", third["capabilities"])
	}
	found := false
	for _, c := range caps {
		if c == "test_new_capability" {
			found = true
		}
	}
	if !found {
		t.Errorf("re-emitted capabilities missing the new tag; got %v", caps)
	}
	if _, present := third["meta_delta"]; present {
		t.Errorf("re-emit response must not carry meta_delta")
	}

	// And the call after that goes back to delta.
	fourth := metaOf(t, srv.jsonResultWithMeta(map[string]any{}, time.Now(), "search", map[string]any{}, 0))
	if _, present := fourth["capabilities"]; present {
		t.Errorf("post-re-emit response must omit capabilities again")
	}
}

// Kill-switch: PINCHER_META_DELTA=0 restores legacy every-call
// emission — graceful degradation for consumers that read
// _meta.capabilities on every response. Not parallel: env is
// process-global (same pattern as TestMetaLite_EnvVar).
func TestMetaDelta_KillSwitch_RestoresEveryCallEmission(t *testing.T) {
	prev, had := os.LookupEnv("PINCHER_META_DELTA")
	t.Cleanup(func() {
		if had {
			os.Setenv("PINCHER_META_DELTA", prev)
		} else {
			os.Unsetenv("PINCHER_META_DELTA")
		}
	})
	if err := os.Setenv("PINCHER_META_DELTA", "0"); err != nil {
		t.Fatalf("setenv: %v", err)
	}

	srv, _, _ := newTestServer(t)
	for i := 0; i < 3; i++ {
		meta := metaOf(t, srv.jsonResultWithMeta(
			map[string]any{"ok": true}, time.Now(), "search", map[string]any{}, 0))
		caps, ok := meta["capabilities"].([]any)
		if !ok || len(caps) == 0 {
			t.Fatalf("call %d: PINCHER_META_DELTA=0 must stamp capabilities on every response; got %v", i+1, meta["capabilities"])
		}
		if _, present := meta["meta_delta"]; present {
			t.Errorf("call %d: kill-switch path must not stamp meta_delta", i+1)
		}
	}
}

// Interaction: PINCHER_META_CAPABILITIES=off (#1087) still wins — no
// capabilities on any response, and no meta_delta marker either (the
// field wasn't omitted by the delta, it was opted out entirely).
func TestMetaDelta_RespectsCapabilitiesOptOut(t *testing.T) {
	prev, had := os.LookupEnv("PINCHER_META_CAPABILITIES")
	t.Cleanup(func() {
		if had {
			os.Setenv("PINCHER_META_CAPABILITIES", prev)
		} else {
			os.Unsetenv("PINCHER_META_CAPABILITIES")
		}
	})
	if err := os.Setenv("PINCHER_META_CAPABILITIES", "off"); err != nil {
		t.Fatalf("setenv: %v", err)
	}

	srv, _, _ := newTestServer(t)
	for i := 0; i < 2; i++ {
		meta := metaOf(t, srv.jsonResultWithMeta(
			map[string]any{"ok": true}, time.Now(), "search", map[string]any{}, 0))
		if _, present := meta["capabilities"]; present {
			t.Errorf("call %d: PINCHER_META_CAPABILITIES=off must drop capabilities", i+1)
		}
		if _, present := meta["meta_delta"]; present {
			t.Errorf("call %d: opt-out path must not stamp meta_delta — nothing was delta-omitted", i+1)
		}
	}
}

// Parse table for the env kill-switch — mirrors parseCapabilitiesEnv's
// tolerance (case, whitespace, aliases; unknown defaults to on).
func TestParseMetaDeltaEnv_Cases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"on", true},
		{"1", true},
		{"0", false},
		{"off", false},
		{"OFF", false},
		{"  off  ", false},
		{"false", false},
		{"none", false},
		{"no", false},
		{"banana", true}, // typo'd opt-out keeps delta on (failure-as-pedagogy)
	}
	for _, c := range cases {
		if got := parseMetaDeltaEnv(c.in); got != c.want {
			t.Errorf("parseMetaDeltaEnv(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

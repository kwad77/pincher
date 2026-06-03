// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// #534: action=set on /v1/adr enforces key ≤256 chars and value ≤16
// KB. Pre-fix arbitrary input was accepted; a paste-of-an-entire-
// transcript blew up the row size and a malformed body returned 500.
// The bound matches the dashboard textarea's maxlength attribute so
// validation is consistent across surfaces.

func TestADR_Set_RejectsOverlongKey(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1"})

	args, _ := json.Marshal(map[string]any{
		"action": "set",
		"key":    strings.Repeat("k", 257),
		"value":  "ok",
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "adr", Arguments: args,
	}}
	r, err := srv.handleADR(context.Background(), req)
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !r.IsError {
		t.Fatalf("expected IsError, got success: %s", textOf(t, r))
	}
	body := textOf(t, r)
	if !strings.Contains(body, "key too long") {
		t.Errorf("expected 'key too long' message, got: %s", body)
	}
	if !strings.Contains(body, "256") {
		t.Errorf("expected the limit (256) in the message, got: %s", body)
	}
}

func TestADR_Set_RejectsOverlongValue(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1"})

	args, _ := json.Marshal(map[string]any{
		"action": "set",
		"key":    "huge",
		"value":  strings.Repeat("v", 16*1024+1),
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "adr", Arguments: args,
	}}
	r, err := srv.handleADR(context.Background(), req)
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if !r.IsError {
		t.Fatalf("expected IsError, got success: %s", textOf(t, r))
	}
	body := textOf(t, r)
	if !strings.Contains(body, "value too long") {
		t.Errorf("expected 'value too long' message, got: %s", body)
	}
	if !strings.Contains(body, "16384") {
		t.Errorf("expected the limit (16384) in the message, got: %s", body)
	}
}

func TestADR_Set_AcceptsExactlyAtLimit(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "p1"
	store.UpsertProject(db.Project{ID: "p1", Path: "/tmp/p1", Name: "p1"})

	// Boundary case: 256 + 16384 must succeed (limits are inclusive).
	args, _ := json.Marshal(map[string]any{
		"action": "set",
		"key":    strings.Repeat("k", 256),
		"value":  strings.Repeat("v", 16*1024),
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: "adr", Arguments: args,
	}}
	r, err := srv.handleADR(context.Background(), req)
	if err != nil {
		t.Fatalf("handleADR: %v", err)
	}
	if r.IsError {
		t.Fatalf("at-limit input should be accepted; got error: %s", textOf(t, r))
	}
}

// #534: dashboard form maxlength attributes match the backend bounds.
// If they drift, server-side rejection turns into a confusing UX where
// the form silently accepts then errors on submit.
func TestDashboard_ADRFormMaxlengthMatchesBackend(t *testing.T) {
	t.Parallel()
	html := renderDashboard("")
	if !strings.Contains(html, `id="adr-key" type="text" maxlength="256"`) {
		t.Errorf("dashboard ADR key input missing maxlength=256 (#534)")
	}
	if !strings.Contains(html, `id="adr-val" maxlength="16384"`) {
		t.Errorf("dashboard ADR value textarea missing maxlength=16384 (#534)")
	}
	// The live counter element must exist or the JS update path crashes.
	if !strings.Contains(html, `id="adr-val-counter"`) {
		t.Errorf("dashboard ADR value counter element missing (#534)")
	}
}

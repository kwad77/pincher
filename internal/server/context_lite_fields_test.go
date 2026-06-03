// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1031: context's lite=true path used projectFields (silent drop on
// unknown field names) instead of projectAndCheckFields. A typo'd
// field like `fields=bogus_field` returned an empty body — no id, no
// source, no warning. Same silent-confidently-wrong shape as #1030
// (search fields projection). Now: unknown fields trip a warning,
// all-bogus falls back to the full lite body.

func setupContextLiteProject(t *testing.T) (*Server, string) {
	t.Helper()
	srv, store, _ := newTestServer(t)
	pid := "p-context-lite"
	store.UpsertProject(db.Project{ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = "/tmp/" + pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: pid + "::pkg.LiteProbe#Function", ProjectID: pid, FilePath: "f.go",
			Name: "LiteProbe", QualifiedName: "pkg.LiteProbe", Kind: "Function", Language: "Go",
			Signature: "func LiteProbe()", ExtractionConfidence: 1.0},
	})
	return srv, pid
}

func TestHandleContext_LiteBogusFields_WarnsAndReturnsFullBody(t *testing.T) {
	t.Parallel()
	srv, pid := setupContextLiteProject(t)

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":     pid + "::pkg.LiteProbe#Function",
		"lite":   true,
		"fields": "bogus_field_name",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, result)
	ws := warningsFromMeta(body)
	foundWarn := false
	for _, w := range ws {
		s, _ := w.(string)
		if strings.Contains(s, "bogus_field_name") && strings.Contains(s, "matched no keys") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected warning naming bogus_field_name; got warnings=%v", ws)
	}
	// Fallback should preserve at least `id` so the response stays useful.
	if _, has := body["id"]; !has {
		t.Errorf("fallback should preserve id field; got body keys=%v", keysOf(body))
	}
}

func TestHandleContext_LiteValidFields_NoWarning(t *testing.T) {
	t.Parallel()
	srv, pid := setupContextLiteProject(t)

	result, err := srv.handleContext(context.Background(), makeReq(map[string]any{
		"id":     pid + "::pkg.LiteProbe#Function",
		"lite":   true,
		"fields": "id",
	}))
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	body := decode(t, result)
	ws := warningsFromMeta(body)
	for _, w := range ws {
		s, _ := w.(string)
		if strings.Contains(s, "matched no keys") {
			t.Errorf("valid fields must not trip the warning; got %s", s)
		}
	}
	if _, has := body["id"]; !has {
		t.Errorf("requested id should be present; got body keys=%v", keysOf(body))
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

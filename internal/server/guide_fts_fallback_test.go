// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

func TestHandleGuide_ValidProject_AutoRunsFTSSymbolFallback(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "guide-fts-sess"
	store.UpsertProject(db.Project{ID: "guide-fts-sess", Path: "/tmp/guide-fts-sess", Name: "guide-fts-sess", IndexedAt: time.Now()})
	mustUpsertSymbols(t, store, []db.Symbol{
		{ID: "guide-fts-sess::src/auth.ts::bootstrapLogin#Function", ProjectID: "guide-fts-sess", FilePath: "src/auth.ts", Name: "bootstrapLogin", QualifiedName: "src.auth.bootstrapLogin", Kind: "Function", Language: "TypeScript", StartByte: 0, EndByte: 80, StartLine: 1, EndLine: 4, Signature: "export function bootstrapLogin()", ExtractionConfidence: 0.95},
	})

	res, err := srv.handleGuide(context.Background(), makeReq(map[string]any{
		"task":    "bootstrapLogin",
		"project": "guide-fts-sess",
	}))
	if err != nil {
		t.Fatalf("handleGuide: %v", err)
	}
	body := decode(t, res)
	matches, _ := body["symbol_matches"].([]any)
	if len(matches) == 0 {
		t.Fatalf("expected guide to auto-run FTS and return symbol_matches; body=%v", body)
	}
	first, _ := matches[0].(map[string]any)
	if got, _ := first["name"].(string); got != "bootstrapLogin" {
		t.Fatalf("first symbol match name = %q, want bootstrapLogin; matches=%v", got, matches)
	}
	if got := strings.Join(metaWarnings(t, body), "\n"); !strings.Contains(got, "auto-ran FTS fallback") {
		t.Fatalf("expected guide FTS fallback warning; got %q", got)
	}
}

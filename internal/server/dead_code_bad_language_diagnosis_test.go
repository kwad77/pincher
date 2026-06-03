// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #1093: pre-fix, dead_code with a language filter that matched zero
// symbols (e.g. language="BogusLang" or a real-but-not-indexed-here
// language like "Ruby") fell into the generic min_confidence-suggestion
// branch — telling the agent to lower a threshold that has no effect
// on the empty result. The real cause is the filter; diagnosis must
// name it.

func TestHandleDeadCode_BadLanguage_DiagnosisNamesFilter(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dc-lang"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 5, SymCount: 5, EdgeCount: 1,
	})
	srv.sessionID = pid

	syms := []db.Symbol{}
	for i := 0; i < 5; i++ {
		syms = append(syms, db.Symbol{
			ID:                   pid + "::pkg.F" + string(rune('A'+i)) + "#Function",
			ProjectID:            pid,
			FilePath:             "a.go",
			Name:                 "F",
			QualifiedName:        "pkg.F",
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		})
	}
	mustUpsertSymbols(t, store, syms)

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"language": "BogusLang",
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope; got %v", body)
	}
	diagnosis, _ := meta["diagnosis"].(string)
	if !strings.Contains(diagnosis, "BogusLang") {
		t.Errorf("diagnosis must name the bad language filter; got %q", diagnosis)
	}
	if strings.Contains(diagnosis, "min_confidence") {
		t.Errorf("diagnosis must NOT suggest min_confidence (filter is the real cause); got %q", diagnosis)
	}
	if !strings.Contains(diagnosis, "Go") {
		t.Errorf("diagnosis must list available languages (Go is the only one indexed); got %q", diagnosis)
	}
	steps, _ := meta["next_steps"].([]any)
	foundDropFilter := false
	for _, s := range steps {
		stepMap, _ := s.(map[string]any)
		args, _ := stepMap["args"].(string)
		if args == "{}" {
			foundDropFilter = true
			break
		}
	}
	if !foundDropFilter {
		t.Errorf("expected a next_step with args={} (drop the filter); got %v", steps)
	}
}

// Control: real language filter with no dead code keeps the
// min_confidence-suggestion diagnosis branch (no regression).
func TestHandleDeadCode_RealLanguage_NoDead_KeepsMinConfidenceAdvice(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := "p-dc-real"
	store.UpsertProject(db.Project{
		ID: pid, Path: t.TempDir(), Name: pid, IndexedAt: time.Now(),
		FileCount: 1, SymCount: 1, EdgeCount: 1,
	})
	srv.sessionID = pid
	mustUpsertSymbols(t, store, []db.Symbol{
		{
			ID:                   pid + "::pkg.F#Function",
			ProjectID:            pid,
			FilePath:             "a.go",
			Name:                 "F",
			QualifiedName:        "pkg.F",
			Kind:                 "Function",
			Language:             "Go",
			ExtractionConfidence: 1.0,
		},
	})

	res, err := srv.handleDeadCode(context.Background(), makeReq(map[string]any{
		"language": "Go",
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	body := decode(t, res)
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		return // no _meta is acceptable
	}
	diagnosis, _ := meta["diagnosis"].(string)
	// Either the gap-coverage diagnosis or the min_confidence one is
	// fine here — what matters is we DIDN'T hit the bad-language path
	// when the language is real.
	if strings.Contains(diagnosis, "matched no symbols") {
		t.Errorf("real language Go must NOT trip the bad-language diagnosis; got %q", diagnosis)
	}
}

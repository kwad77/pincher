// SPDX-License-Identifier: MIT

package cypher

import (
	"context"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// seedSymbolsForEnumTest plants a tiny corpus with two known kinds and
// two known languages so the enum-value diagnostic has something
// concrete to compare against.
func seedSymbolsForEnumTest(t *testing.T) (*db.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	const projectID = "p1"
	if err := store.UpsertProject(db.Project{ID: projectID, Path: "/p1", Name: "p1"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := store.BulkUpsertSymbols([]db.Symbol{
		{ID: projectID + "::a.foo#Function", ProjectID: projectID, FilePath: "a.go",
			Name: "foo", QualifiedName: "a.foo", Kind: "Function", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: projectID + "::a.Bar#Method", ProjectID: projectID, FilePath: "a.go",
			Name: "Bar", QualifiedName: "a.Bar", Kind: "Method", Language: "Go",
			ExtractionConfidence: 1.0},
		{ID: projectID + "::b.x#Variable", ProjectID: projectID, FilePath: "b.py",
			Name: "x", QualifiedName: "b.x", Kind: "Variable", Language: "Python",
			ExtractionConfidence: 1.0},
	}); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	return store, projectID
}

// #501: a 0-row query that filters on kind = 'init' should warn that
// 'init' isn't a known kind in the project, listing the actual kinds.
func TestExecute_BogusKindValue_FiresWarning(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.kind = 'init' RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("expected 0 rows; got %d", res.Total)
	}
	if len(res.Warnings) == 0 {
		t.Fatalf("expected enum-value warning; got none")
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "kind") {
		t.Errorf("warning should mention 'kind'; got %q", joined)
	}
	if !strings.Contains(joined, "'init'") && !strings.Contains(joined, "\"init\"") {
		t.Errorf("warning should quote the offending value 'init'; got %q", joined)
	}
	// Should list known kinds in the project.
	for _, want := range []string{"Function", "Method", "Variable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning should list known kind %q; got %q", want, joined)
		}
	}
}

// #501: a 0-row query that filters on language = 'JavaScript' (not in
// the project) should warn and suggest known languages.
func TestExecute_BogusLanguageValue_FiresWarning(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n) WHERE n.language = 'JavaScript' RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("expected 0 rows; got %d", res.Total)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "language") || !strings.Contains(joined, "JavaScript") {
		t.Errorf("warning should reference language=JavaScript; got %q", joined)
	}
	if !strings.Contains(joined, "Go") || !strings.Contains(joined, "Python") {
		t.Errorf("warning should list project's languages (Go, Python); got %q", joined)
	}
}

// A 0-row query whose enum filter IS valid (e.g. kind='Function' but
// no Functions named 'nonexistent') should NOT fire an enum-value
// warning. The 0-row result is real — not an enum typo.
func TestExecute_ValidKindValue_NoEnumWarning(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n) WHERE n.kind = 'Function' AND n.name = 'nonexistent' RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("expected 0 rows; got %d", res.Total)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "Known kind") || strings.Contains(w, "Known language") {
			t.Errorf("valid kind value 'Function' must not emit enum warning; got %q", w)
		}
	}
}

// A non-empty result must NOT trigger an enum-value warning even when
// the filter value matches some symbols. Negative space: the diagnostic
// is gated on Total == 0 to avoid noise.
func TestExecute_NonEmptyResult_NoEnumWarning(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n) WHERE n.kind = 'Function' RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total == 0 {
		t.Fatalf("expected at least one Function; got 0")
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "Known kind") {
			t.Errorf("non-empty result must not emit enum warning; got %q", w)
		}
	}
}

// 'label' is the cypher alias for 'kind'. Bogus value via the alias
// should still fire — the warning surfaces the resolved column name.
func TestExecute_BogusLabelAlias_FiresWarning(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n) WHERE n.label = 'WrongKind' RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("expected 0 rows; got %d", res.Total)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "kind") || !strings.Contains(joined, "WrongKind") {
		t.Errorf("alias 'label' should resolve to 'kind' in warning; got %q", joined)
	}
}

// MATCH pattern labels are kinds too — `MATCH (n:Funtion)` typo
// must surface the same warning as `WHERE n.kind = 'Funtion'`.
func TestExecute_MatchLabelTypo_FiresWarning(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n:Funtion) RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("expected 0 rows on typo; got %d", res.Total)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "kind") || !strings.Contains(joined, "Funtion") {
		t.Errorf("MATCH (n:Funtion) should warn on kind=Funtion; got %q", joined)
	}
}

// Valid MATCH pattern label must NOT trigger an enum warning even
// when the result is empty for other reasons.
func TestExecute_ValidMatchLabel_NoEnumWarning(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n:Function) WHERE n.name = 'nonexistent' RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("expected 0 rows; got %d", res.Total)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "Known kind") {
			t.Errorf("valid MATCH label 'Function' must not emit enum warning; got %q", w)
		}
	}
}

// Two distinct enum-value typos in one query should produce two
// warnings, one per offender, in stable order.
func TestExecute_MultipleEnumTypos_OneWarningEach(t *testing.T) {
	store, pid := seedSymbolsForEnumTest(t)
	ex := &Executor{DB: store.DB(), ProjectID: pid}

	res, err := ex.Execute(context.Background(),
		`MATCH (n) WHERE n.kind = 'init' AND n.language = 'Bash' RETURN n.name`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("expected 0 rows; got %d", res.Total)
	}
	if len(res.Warnings) < 2 {
		t.Fatalf("expected at least 2 enum-value warnings; got %d: %v",
			len(res.Warnings), res.Warnings)
	}
}

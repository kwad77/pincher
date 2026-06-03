// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #276: health emits actionable next_steps.

func TestSuggestHealthNextSteps_NoProject(t *testing.T) {
	t.Parallel()
	report := &db.HealthReport{}
	steps := suggestHealthNextSteps(report)
	if len(steps) == 0 {
		t.Fatal("expected at least one next_step when no project resolved")
	}
	if steps[0]["tool"] != "list" {
		t.Errorf("first step = %q, want list", steps[0]["tool"])
	}
}

func TestSuggestHealthNextSteps_StaleIndex(t *testing.T) {
	t.Parallel()
	report := &db.HealthReport{
		Project:        &db.Project{Name: "x", Path: "/tmp/x", SymCount: 50, IndexedAt: time.Now()},
		StalenessSecs:  7200, // 2h
		StalenessHuman: "2h",
	}
	steps := suggestHealthNextSteps(report)
	foundIndex := false
	for _, s := range steps {
		if s["tool"] == "index" {
			foundIndex = true
			break
		}
	}
	if !foundIndex {
		t.Errorf("stale index should suggest tool=index; steps=%v", steps)
	}
}

func TestSuggestHealthNextSteps_LowConfidence(t *testing.T) {
	t.Parallel()
	report := &db.HealthReport{
		Project: &db.Project{Name: "x", Path: "/tmp/x", SymCount: 50},
		Coverage: []db.LanguageCoverage{
			{
				Language: "Markdown",
				ByKind: []db.KindCoverage{
					{Kind: "Section", Symbols: 100, P10: 0.65, P50: 0.85},
				},
			},
		},
	}
	steps := suggestHealthNextSteps(report)
	foundSearch := false
	for _, s := range steps {
		if s["tool"] == "search" {
			foundSearch = true
			break
		}
	}
	if !foundSearch {
		t.Errorf("low p10 should suggest tool=search with min_confidence=0; steps=%v", steps)
	}
}

func TestSuggestHealthNextSteps_LargeProjectSuggestsArchitecture(t *testing.T) {
	t.Parallel()
	report := &db.HealthReport{
		Project: &db.Project{Name: "x", Path: "/tmp/x", SymCount: 500},
	}
	steps := suggestHealthNextSteps(report)
	foundArch := false
	for _, s := range steps {
		if s["tool"] == "architecture" {
			foundArch = true
			break
		}
	}
	if !foundArch {
		t.Errorf("large project should suggest tool=architecture; steps=%v", steps)
	}
}

func TestSuggestHealthNextSteps_HealthyTinyProject(t *testing.T) {
	t.Parallel()
	report := &db.HealthReport{
		Project: &db.Project{Name: "x", Path: "/tmp/x", SymCount: 10},
	}
	steps := suggestHealthNextSteps(report)
	if len(steps) != 0 {
		t.Errorf("healthy tiny project should produce no next_steps; got %v", steps)
	}
}

// SPDX-License-Identifier: MIT

package server

import (
	"strings"
	"testing"
)

// settings_flood advisory: a project whose symbol surface is dominated
// by Setting-kind symbols carries the data-artifact flood signature —
// observed in the wild as data/*.json model files (under the 4 MB
// per-file cap) becoming 27,672 junk Setting symbols, 96% of the
// project. The advisory points the user at .pincherignore. These tests
// pin the pure helper's thresholds (> 1000 total symbols AND Setting
// share strictly above 80%) without the DB-backed handler dance,
// mirroring the ghost-advisory tests.

func TestSettingsFloodAdvisory_FlagsFloodedProject(t *testing.T) {
	t.Parallel()
	projects := []doctorProjectSummary{
		{ID: "p1", Name: "model-repo", Symbols: 28800},
		{ID: "p2", Name: "healthy-repo", Symbols: 5000},
	}
	counts := map[string]int{
		"p1": 27672, // 96% — the observed pathological case
		"p2": 200,   // 4% — generous-but-normal config share
	}
	got := settingsFloodAdvisory(projects, counts)
	if got == "" {
		t.Fatal("expected advisory for 96%-Settings project; got empty")
	}
	if !strings.Contains(got, "model-repo") {
		t.Errorf("advisory must name the flooded project; got %q", got)
	}
	if strings.Contains(got, "healthy-repo") {
		t.Errorf("advisory must NOT name a healthy project; got %q", got)
	}
	if !strings.Contains(got, ".pincherignore") {
		t.Errorf("advisory must recommend .pincherignore; got %q", got)
	}
	if !strings.Contains(got, "re-index") {
		t.Errorf("advisory must mention the re-index step; got %q", got)
	}
}

func TestSettingsFloodAdvisory_ExactlyEightyPercentStaysSilent(t *testing.T) {
	t.Parallel()
	// Share must be STRICTLY above 80% — a repo sitting exactly at the
	// boundary is not flagged.
	projects := []doctorProjectSummary{
		{ID: "p1", Name: "boundary-repo", Symbols: 2000},
	}
	counts := map[string]int{"p1": 1600} // exactly 80%
	if got := settingsFloodAdvisory(projects, counts); got != "" {
		t.Errorf("expected NO advisory at exactly 80%% Setting share; got %q", got)
	}
}

func TestSettingsFloodAdvisory_SmallProjectStaysSilent(t *testing.T) {
	t.Parallel()
	// Below 1000 total symbols a genuinely config-heavy repo (dotfiles,
	// Ansible inventory) can legitimately be nearly all Settings.
	projects := []doctorProjectSummary{
		{ID: "p1", Name: "dotfiles", Symbols: 900},
	}
	counts := map[string]int{"p1": 890} // 99% but tiny
	if got := settingsFloodAdvisory(projects, counts); got != "" {
		t.Errorf("expected NO advisory below 1000-symbol threshold; got %q", got)
	}
}

func TestSettingsFloodAdvisory_AllHealthy_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	projects := []doctorProjectSummary{
		{ID: "a", Name: "a", Symbols: 50000},
		{ID: "b", Name: "b", Symbols: 2000},
	}
	counts := map[string]int{"a": 4000, "b": 0}
	if got := settingsFloodAdvisory(projects, counts); got != "" {
		t.Errorf("expected empty advisory for healthy projects; got %q", got)
	}
}

func TestSettingsFloodAdvisory_CapsAtWorstThree(t *testing.T) {
	t.Parallel()
	// projects arrive sorted by symbol count desc (handleDoctor's
	// contract); the advisory names at most 3 so it stays scannable.
	projects := []doctorProjectSummary{
		{ID: "p1", Name: "flood-one", Symbols: 40000},
		{ID: "p2", Name: "flood-two", Symbols: 30000},
		{ID: "p3", Name: "flood-three", Symbols: 20000},
		{ID: "p4", Name: "flood-four", Symbols: 10000},
	}
	counts := map[string]int{"p1": 39000, "p2": 29000, "p3": 19000, "p4": 9500}
	got := settingsFloodAdvisory(projects, counts)
	if got == "" {
		t.Fatal("expected advisory; got empty")
	}
	for _, want := range []string{"flood-one", "flood-two", "flood-three"} {
		if !strings.Contains(got, want) {
			t.Errorf("advisory must name %s; got %q", want, got)
		}
	}
	if strings.Contains(got, "flood-four") {
		t.Errorf("advisory must cap at the worst 3 projects; got %q", got)
	}
}

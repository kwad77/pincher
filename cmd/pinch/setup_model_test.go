package main

import (
	"strings"
	"testing"

	pinit "github.com/kwad77/pincher/internal/init"
)

// setup_model_test.go — exercises the pure `pincher setup` wizard state
// machine (#1710 v0.92). Terminal I/O lives in setup.go and is not
// covered here; this file proves the transitions and selection logic.

func TestSetupUsageAndHelpArgs(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		if !setupArgsWantHelp([]string{arg}) {
			t.Fatalf("setupArgsWantHelp(%q) = false, want true", arg)
		}
	}
	if setupArgsWantHelp(nil) || setupArgsWantHelp([]string{"--target=claude"}) {
		t.Fatal("setupArgsWantHelp matched non-help args")
	}

	var out strings.Builder
	printSetupUsage(&out)
	for _, want := range []string{
		"usage: pincher setup",
		"Interactive install wizard",
		"pincher init --target=<name>",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("setup usage missing %q:\n%s", want, out.String())
		}
	}
}

func targetIndex(targets []pinit.Target, name string) int {
	for i, t := range targets {
		if t.Name == name {
			return i
		}
	}
	return -1
}

// newSetupModel pre-checks every detected host and leaves the rest off.
func TestSetupModel_DetectedTargetsPreSelected(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget, pinit.CursorTarget}, false)
	if !m.selected["claude"] || !m.selected["cursor"] {
		t.Errorf("detected targets must start checked; got %v", m.selected)
	}
	if m.selected["zed"] {
		t.Error("an undetected target must start unchecked")
	}
	if !m.anySelected() {
		t.Error("anySelected should be true with two detected hosts")
	}
}

// moveCursor clamps to [0, itemCount-1] on the hosts screen.
func TestSetupModel_CursorClamps(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, nil, false)
	m.moveCursor(-5)
	if m.cursor != 0 {
		t.Errorf("cursor below 0 should clamp to 0; got %d", m.cursor)
	}
	m.moveCursor(9999)
	if m.cursor != len(pinit.AllTargets)-1 {
		t.Errorf("cursor past end should clamp to %d; got %d", len(pinit.AllTargets)-1, m.cursor)
	}
}

// space toggles the host under the cursor; bulk actions set the set.
func TestSetupModel_ToggleAndBulkSelect(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, false)

	m.cursor = targetIndex(pinit.AllTargets, "zed")
	m.toggleAtCursor()
	if !m.selected["zed"] {
		t.Error("toggleAtCursor should have checked zed")
	}
	m.toggleAtCursor()
	if m.selected["zed"] {
		t.Error("a second toggle should have unchecked zed")
	}

	m.selectAll()
	for _, tg := range pinit.AllTargets {
		if !m.selected[tg.Name] {
			t.Fatalf("selectAll missed %s", tg.Name)
		}
	}
	m.selectNone()
	if m.anySelected() {
		t.Error("selectNone left something checked")
	}
	m.selectDetected()
	if !m.selected["claude"] {
		t.Error("selectDetected should restore the detected set (claude)")
	}
	if m.selected["zed"] {
		t.Error("selectDetected should clear undetected targets")
	}
}

// optionKeys surfaces only the options relevant to the current
// selection and environment.
func TestSetupModel_OptionKeysAreContextual(t *testing.T) {
	// Claude selected + git repo → all three options.
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, true)
	keys := m.optionKeys()
	want := map[string]bool{optGlobalKey: true, optHookKey: true, optGitHooksKey: true}
	if len(keys) != 3 {
		t.Fatalf("claude + git repo: want 3 option keys, got %v", keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected option key %q", k)
		}
	}
	if !m.hasOptions() {
		t.Error("hasOptions should be true")
	}

	// Cursor only, no git repo → no options (cursor has no global file,
	// no Claude hook, and there's no .git).
	m2 := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.CursorTarget}, false)
	if keys := m2.optionKeys(); len(keys) != 0 {
		t.Errorf("cursor-only non-git: want 0 option keys, got %v", keys)
	}
	if m2.hasOptions() {
		t.Error("hasOptions should be false for a cursor-only non-git selection")
	}
}

// advance skips the options screen when nothing is configurable, and
// is a no-op while the hosts screen has an empty selection.
func TestSetupModel_AdvanceScreenFlow(t *testing.T) {
	// No selection → advance is a no-op on the hosts screen.
	empty := newSetupModel(pinit.AllTargets, nil, false)
	if empty.advance(); empty.screen != scrHosts {
		t.Errorf("advance with nothing selected must stay on hosts; got %d", empty.screen)
	}

	// Claude + git → hosts advances to the options screen.
	withOpts := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, true)
	withOpts.advance()
	if withOpts.screen != scrOptions {
		t.Fatalf("claude+git should advance hosts → options; got %d", withOpts.screen)
	}
	withOpts.advance()
	if withOpts.screen != scrConfirm {
		t.Fatalf("options should advance → confirm; got %d", withOpts.screen)
	}
	if runApply := withOpts.advance(); !runApply {
		t.Error("advance from confirm must signal runApply=true")
	}

	// Cursor-only non-git → hosts advances straight to confirm (options
	// screen skipped).
	noOpts := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.CursorTarget}, false)
	noOpts.advance()
	if noOpts.screen != scrConfirm {
		t.Fatalf("cursor-only non-git should skip options, hosts → confirm; got %d", noOpts.screen)
	}
}

// back mirrors advance — including the options-screen skip.
func TestSetupModel_BackMirrorsAdvance(t *testing.T) {
	withOpts := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, true)
	withOpts.screen = scrConfirm
	withOpts.back()
	if withOpts.screen != scrOptions {
		t.Errorf("back from confirm with options should land on options; got %d", withOpts.screen)
	}
	withOpts.back()
	if withOpts.screen != scrHosts {
		t.Errorf("back from options should land on hosts; got %d", withOpts.screen)
	}

	noOpts := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.CursorTarget}, false)
	noOpts.screen = scrConfirm
	noOpts.back()
	if noOpts.screen != scrHosts {
		t.Errorf("back from confirm with no options should skip straight to hosts; got %d", noOpts.screen)
	}
}

// selectedTargets returns the checked targets in registry order.
func TestSetupModel_SelectedTargetsRegistryOrder(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, nil, false)
	m.selected["zed"] = true
	m.selected["claude"] = true
	got := m.selectedTargets()
	if len(got) != 2 {
		t.Fatalf("want 2 selected targets, got %d", len(got))
	}
	// AllTargets lists Claude before Zed — order must be preserved.
	if got[0].Name != "claude" || got[1].Name != "zed" {
		t.Errorf("selectedTargets must follow registry order; got %s, %s", got[0].Name, got[1].Name)
	}
}

// toggleAtCursor on the options screen flips the right toggle.
func TestSetupModel_OptionToggle(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, true)
	m.screen = scrOptions
	keys := m.optionKeys() // [global, hook, githooks]

	hookIdx := -1
	for i, k := range keys {
		if k == optHookKey {
			hookIdx = i
		}
	}
	if hookIdx < 0 {
		t.Fatal("hook option missing")
	}
	if !m.optHook {
		t.Fatal("hook should default on")
	}
	m.cursor = hookIdx
	m.toggleAtCursor()
	if m.optHook {
		t.Error("toggling the hook option should have turned it off")
	}
}

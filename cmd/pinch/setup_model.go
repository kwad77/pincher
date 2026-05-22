package main

import (
	pinit "github.com/kwad77/pincher/internal/init"
)

// setup_model.go — the pure state machine behind `pincher setup`, the
// interactive install wizard (#1710 v0.92). All terminal I/O,
// filesystem writes, and rendering live in setup.go; this file is
// dependency-free state + transitions so it can be unit-tested without
// a TTY.

type setupScreen int

const (
	scrHosts setupScreen = iota // checklist of editor/agent targets
	scrOptions                  // global / hook / git-hooks toggles
	scrConfirm                  // file-write plan preview
	scrDone                     // results + next steps
)

// Option keys for the options screen. Only the applicable subset is
// shown — see optionKeys.
const (
	optGlobalKey   = "global"
	optHookKey     = "hook"
	optGitHooksKey = "githooks"
)

// applyResult is one line of the post-apply summary.
type applyResult struct {
	name  string
	label string
	ok    bool
}

// setupModel is the wizard's entire state. Construct via newSetupModel.
type setupModel struct {
	screen  setupScreen
	targets []pinit.Target  // AllTargets, in registry order
	detected map[string]bool // target name → editor marker found under cwd
	// configured: target name → pincher's policy block is ALREADY in
	// that host's config (Plan reports action "updated"). Set by
	// runSetupCLI after construction; nil is fine — a nil-map read is
	// false. Distinguishes "editor installed" from "pincher wired up".
	configured map[string]bool
	selected   map[string]bool // target name → chosen for install
	cursor     int             // row index within the current screen

	inGitRepo bool

	// options-screen state
	optGlobal   bool
	optHook     bool // Claude PreToolUse hook; default on
	optGitHooks bool

	// populated by setup.go's apply step before the done screen
	results    []applyResult
	indexAfter bool
	quit       bool
}

// newSetupModel seeds the wizard: every detected target starts checked.
func newSetupModel(targets, detected []pinit.Target, inGitRepo bool) setupModel {
	det := make(map[string]bool, len(detected))
	for _, t := range detected {
		det[t.Name] = true
	}
	sel := make(map[string]bool, len(det))
	for name := range det {
		sel[name] = true
	}
	return setupModel{
		screen:    scrHosts,
		targets:   targets,
		detected:  det,
		selected:  sel,
		inGitRepo: inGitRepo,
		optHook:   true, // mirrors `pincher init`'s hook-on default
	}
}

// anySelected reports whether at least one target is checked.
func (m *setupModel) anySelected() bool {
	for _, t := range m.targets {
		if m.selected[t.Name] {
			return true
		}
	}
	return false
}

// anySelectedSupportsGlobal reports whether the global-rules toggle is
// meaningful for the current selection.
func (m *setupModel) anySelectedSupportsGlobal() bool {
	for _, t := range m.targets {
		if m.selected[t.Name] && t.SupportsGlobal && !t.AlwaysGlobal {
			return true
		}
	}
	return false
}

// optionKeys returns the applicable options for the current selection,
// in display order. Empty → the options screen is skipped entirely.
func (m *setupModel) optionKeys() []string {
	var keys []string
	if m.anySelectedSupportsGlobal() {
		keys = append(keys, optGlobalKey)
	}
	if m.selected["claude"] {
		keys = append(keys, optHookKey)
	}
	if m.inGitRepo {
		keys = append(keys, optGitHooksKey)
	}
	return keys
}

func (m *setupModel) hasOptions() bool { return len(m.optionKeys()) > 0 }

// optionValue reads the toggle backing an option key.
func (m *setupModel) optionValue(key string) bool {
	switch key {
	case optGlobalKey:
		return m.optGlobal
	case optHookKey:
		return m.optHook
	case optGitHooksKey:
		return m.optGitHooks
	}
	return false
}

// itemCount is the number of selectable rows on the current screen.
func (m *setupModel) itemCount() int {
	switch m.screen {
	case scrHosts:
		return len(m.targets)
	case scrOptions:
		return len(m.optionKeys())
	default:
		return 0
	}
}

// moveCursor shifts the cursor by delta, clamped to the current screen.
func (m *setupModel) moveCursor(delta int) {
	n := m.itemCount()
	if n == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > n-1 {
		m.cursor = n - 1
	}
}

// toggleAtCursor flips the checkbox under the cursor (hosts: a target;
// options: an option key). No-op on screens with no checkboxes.
func (m *setupModel) toggleAtCursor() {
	switch m.screen {
	case scrHosts:
		if m.cursor >= 0 && m.cursor < len(m.targets) {
			name := m.targets[m.cursor].Name
			m.selected[name] = !m.selected[name]
		}
	case scrOptions:
		keys := m.optionKeys()
		if m.cursor >= 0 && m.cursor < len(keys) {
			switch keys[m.cursor] {
			case optGlobalKey:
				m.optGlobal = !m.optGlobal
			case optHookKey:
				m.optHook = !m.optHook
			case optGitHooksKey:
				m.optGitHooks = !m.optGitHooks
			}
		}
	}
}

// selectAll / selectDetected / selectNone are hosts-screen bulk actions.
func (m *setupModel) selectAll() {
	for _, t := range m.targets {
		m.selected[t.Name] = true
	}
}

func (m *setupModel) selectDetected() {
	for _, t := range m.targets {
		m.selected[t.Name] = m.detected[t.Name]
	}
}

func (m *setupModel) selectNone() {
	for k := range m.selected {
		m.selected[k] = false
	}
}

// advance moves to the next screen. From hosts it skips the options
// screen when no option applies. Returns true when the caller should
// run the apply step (the confirm → done transition is effect-bearing,
// so setup.go owns it). A hosts screen with nothing selected is a
// no-op.
func (m *setupModel) advance() (runApply bool) {
	switch m.screen {
	case scrHosts:
		if !m.anySelected() {
			return false
		}
		if m.hasOptions() {
			m.screen = scrOptions
		} else {
			m.screen = scrConfirm
		}
		m.cursor = 0
	case scrOptions:
		m.screen = scrConfirm
		m.cursor = 0
	case scrConfirm:
		return true
	}
	return false
}

// back moves to the previous screen, mirroring advance's skip rule.
func (m *setupModel) back() {
	switch m.screen {
	case scrOptions:
		m.screen = scrHosts
		m.cursor = 0
	case scrConfirm:
		if m.hasOptions() {
			m.screen = scrOptions
		} else {
			m.screen = scrHosts
		}
		m.cursor = 0
	}
}

// selectedTargets returns the checked targets in registry order.
func (m *setupModel) selectedTargets() []pinit.Target {
	out := make([]pinit.Target, 0, len(m.targets))
	for _, t := range m.targets {
		if m.selected[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

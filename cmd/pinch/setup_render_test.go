package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pinit "github.com/kwad77/pincher/internal/init"
)

// setup_render_test.go — covers the pure render + key-decode layer of
// `pincher setup` (#1710 v0.92). The raw-mode terminal plumbing in
// runSetupCLI needs a real TTY and is not covered here.

func joinLines(ls []string) string { return strings.Join(ls, "\n") }

// decodeKey maps every keystroke the wizard understands.
func TestDecodeKey(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want wkey
	}{
		{"up arrow", []byte{0x1b, '[', 'A'}, keyUp},
		{"down arrow", []byte{0x1b, '[', 'B'}, keyDown},
		{"right arrow (ignored)", []byte{0x1b, '[', 'C'}, keyNone},
		{"enter CR", []byte{'\r'}, keyEnter},
		{"enter LF", []byte{'\n'}, keyEnter},
		{"space", []byte{' '}, keySpace},
		{"ctrl-c", []byte{0x03}, keyQuit},
		{"bare esc", []byte{0x1b}, keyBack},
		{"q", []byte{'q'}, keyQuit},
		{"Q upper", []byte{'Q'}, keyQuit},
		{"a", []byte{'a'}, keyAll},
		{"d", []byte{'d'}, keyDetected},
		{"n", []byte{'n'}, keySelNone},
		{"b", []byte{'b'}, keyBack},
		{"i", []byte{'i'}, keyIndex},
		{"unmapped char", []byte{'z'}, keyNone},
		{"empty slice", []byte{}, keyQuit},
	}
	for _, c := range cases {
		if got := decodeKey(c.in); got != c.want {
			t.Errorf("decodeKey(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// renderHosts shows checkboxes, the cursor marker, and a detected tag.
func TestRenderHosts(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, false)
	out := joinLines(renderHosts(&m))

	if !strings.Contains(out, "[x]") {
		t.Error("a detected (pre-selected) host should render a checked box")
	}
	if !strings.Contains(out, "[ ]") {
		t.Error("an unselected host should render an empty box")
	}
	if !strings.Contains(out, "·detected") {
		t.Error("the detected marker should appear for claude")
	}
	if !strings.Contains(out, "›") {
		t.Error("the cursor marker should appear on the current row")
	}
	if !strings.Contains(out, "enter continue") {
		t.Error("the footer hint should be present")
	}

	// Nothing selected → the wizard nudges the user.
	m.selectNone()
	if !strings.Contains(joinLines(renderHosts(&m)), "select at least one") {
		t.Error("an empty selection should render the select-one nudge")
	}
}

// renderOptions lists only the contextual options.
func TestRenderOptions(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, true)
	m.screen = scrOptions
	out := joinLines(renderOptions(&m))
	if !strings.Contains(out, "PreToolUse hook") {
		t.Error("the Claude hook option should render when claude is selected")
	}
	if !strings.Contains(out, "Git hooks") {
		t.Error("the git-hooks option should render in a git repo")
	}
	if !strings.Contains(out, "[x]") {
		t.Error("the hook option defaults on, so a checked box should render")
	}
}

// renderConfirm previews the file-write plan for the selection.
func TestRenderConfirm(t *testing.T) {
	dir := t.TempDir()
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.CursorTarget}, false)
	out := joinLines(renderConfirm(&m, dir))
	if !strings.Contains(out, "cursor") {
		t.Error("the confirm screen should name the selected target")
	}
	if !strings.Contains(out, "enter apply") {
		t.Error("the confirm footer should offer apply")
	}
}

// renderDone shows the apply results and the index offer.
func TestRenderDone(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, nil, false)
	m.screen = scrDone
	m.results = []applyResult{
		{name: "cursor", ok: true, label: "cursor — wrote .cursor/rules/pincher.mdc"},
		{name: "zed", ok: false, label: "zed — write failed"},
	}
	out := joinLines(renderDone(&m))
	if !strings.Contains(out, "✓") || !strings.Contains(out, "✗") {
		t.Error("done screen should mark ok results ✓ and failures ✗")
	}
	if !strings.Contains(out, "index this project") {
		t.Error("done screen should offer to index the project")
	}
}

// renderScreen dispatches to the right per-screen renderer.
func TestRenderScreenDispatch(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, nil, false)
	dir := t.TempDir()
	for _, scr := range []setupScreen{scrHosts, scrOptions, scrConfirm, scrDone} {
		m.screen = scr
		if len(renderScreen(&m, dir)) == 0 {
			t.Errorf("renderScreen for screen %d produced no lines", scr)
		}
	}
}

// optionLabel returns a non-empty human label for each option key, and
// passes unknown keys through.
func TestOptionLabel(t *testing.T) {
	for _, k := range []string{optGlobalKey, optHookKey, optGitHooksKey} {
		if optionLabel(k) == "" || optionLabel(k) == k {
			t.Errorf("optionLabel(%q) should be a human label", k)
		}
	}
	if optionLabel("bogus") != "bogus" {
		t.Error("an unknown key should pass through unchanged")
	}
}

// condenseHome collapses a home-prefixed path to ~/… and leaves other
// paths alone.
func TestCondenseHome(t *testing.T) {
	if got := condenseHome("/nowhere/special/file"); got != "/nowhere/special/file" {
		t.Errorf("a non-home path should be returned unchanged; got %q", got)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		p := filepath.Join(home, "x", "y")
		if got := condenseHome(p); !strings.HasPrefix(got, "~") {
			t.Errorf("a home-prefixed path should collapse to ~/…; got %q", got)
		}
	}
}

// isGitRepo detects a .git entry at the directory root.
func TestIsGitRepo(t *testing.T) {
	dir := t.TempDir()
	if isGitRepo(dir) {
		t.Error("a fresh tempdir is not a git repo")
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if !isGitRepo(dir) {
		t.Error("a directory with a .git entry should be reported as a git repo")
	}
}

// printSetupSummary renders the OK/FAIL result lines.
func TestPrintSetupSummary(t *testing.T) {
	var buf strings.Builder
	setupOut = &buf
	defer func() { setupOut = os.Stdout }()

	m := newSetupModel(pinit.AllTargets, nil, false)
	m.results = []applyResult{
		{name: "cursor", ok: true, label: "cursor — wrote rules"},
		{name: "zed", ok: false, label: "zed — failed"},
	}
	printSetupSummary(&m)
	out := buf.String()
	if !strings.Contains(out, "[OK  ]") || !strings.Contains(out, "[FAIL]") {
		t.Errorf("summary should mark each result OK/FAIL; got:\n%s", out)
	}
}

// tint wraps a string in an SGR code and a reset.
func TestTint(t *testing.T) {
	got := tint(ansiGreen, "ok")
	if got != ansiGreen+"ok"+ansiReset {
		t.Errorf("tint = %q, want code+s+reset", got)
	}
}

// renderHosts colorizes the detected marker and the checked box.
func TestRenderHosts_Colorized(t *testing.T) {
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.ClaudeTarget}, false)
	out := joinLines(renderHosts(&m))
	if !strings.Contains(out, ansiGreen) {
		t.Error("renderHosts should emit a green span for the detected/checked host")
	}
	if !strings.Contains(out, ansiAccent) {
		t.Error("renderHosts should emit the accent color on the cursor row")
	}
}

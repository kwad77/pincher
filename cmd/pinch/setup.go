package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pinit "github.com/kwad77/pincher/internal/init"
	"golang.org/x/term"
)

// setup.go — `pincher setup`, the interactive install wizard (#1710
// v0.92). It is a terminal front-end over the existing `pincher init`
// machinery: pinit.DetectTargets pre-checks installed hosts,
// pinit.Plan / WriteFileEnsuringDir do the writes, and the Claude hook
// + git-hook installers are reused verbatim. All state + transitions
// live in setup_model.go (pure, unit-tested); this file is terminal
// I/O, rendering, and the filesystem effects.
//
// `pincher init --target=...` remains the scripted/non-interactive
// path. setup refuses to run without a TTY and points there.

// ── ANSI ────────────────────────────────────────────────────────────
const (
	ansiHome    = "\x1b[H\x1b[J" // cursor home + clear to end of screen
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiReset   = "\x1b[0m"
	ansiHideCur = "\x1b[?25l"
	ansiShowCur = "\x1b[?25h"
	ansiGreen   = "\x1b[32m"
	ansiRed     = "\x1b[31m"
	ansiAccent  = "\x1b[36m" // cyan — the wizard's highlight color
)

// tint wraps s in an SGR code and a reset. Each call is self-contained
// (no nesting) so colored spans compose cleanly within a row.
func tint(code, s string) string { return code + s + ansiReset }

// wkey is a decoded keystroke.
type wkey int

const (
	keyNone wkey = iota
	keyUp
	keyDown
	keyEnter
	keySpace
	keyQuit
	keyBack
	keyAll
	keyDetected
	keySelNone
	keyIndex
)

// runSetupCLI implements `pincher setup`.
func runSetupCLI(args []string) {
	_ = args // setup takes no flags; arg-free by design (interactive only).

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher setup: cwd: %v\n", err)
		os.Exit(1)
	}

	inFd := int(os.Stdin.Fd())
	outFd := int(os.Stdout.Fd())
	if !term.IsTerminal(inFd) || !term.IsTerminal(outFd) {
		fmt.Fprintln(os.Stderr, "pincher setup is interactive — it needs a real terminal.")
		fmt.Fprintln(os.Stderr, "For scripted or piped installs use: pincher init --target=<name>")
		fmt.Fprintln(os.Stderr, "Run `pincher init -h` to see every target.")
		os.Exit(1)
	}

	m := newSetupModel(pinit.AllTargets, pinit.DetectTargets(cwd), isGitRepo(cwd))

	oldState, err := term.MakeRaw(inFd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher setup: cannot enter raw mode: %v\n", err)
		os.Exit(1)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		fmt.Print(ansiShowCur)
		_ = term.Restore(inFd, oldState)
	}
	defer restore()
	fmt.Print(ansiHideCur)

	runSetupLoop(&m, cwd, readKey)
	restore() // back to a cooked terminal before any normal output

	if m.quit && len(m.results) == 0 {
		fmt.Println("Setup cancelled — nothing was written.")
		return
	}
	printSetupSummary(&m)
	if m.indexAfter {
		fmt.Println()
		fmt.Println("Indexing this project…")
		runIndexCLI(nil)
	}
}

// runSetupLoop drives the render → read-key → transition cycle until
// the wizard finishes or the user quits. nextKey is injectable so the
// loop is testable without a TTY (production passes readKey).
func runSetupLoop(m *setupModel, cwd string, nextKey func() wkey) {
	for {
		paint(renderScreen(m, cwd))
		k := nextKey()

		// q / Ctrl-C cancels from any screen *before* apply has run.
		if k == keyQuit && m.screen != scrDone {
			m.quit = true
			return
		}

		switch m.screen {
		case scrHosts:
			handleHostsKey(m, k)
		case scrOptions:
			handleOptionsKey(m, k)
		case scrConfirm:
			handleConfirmKey(m, k, cwd) // enter → applySetup, then renders scrDone
		case scrDone:
			if k == keyEnter || k == keyQuit {
				return
			}
			if k == keyIndex {
				m.indexAfter = true
				return
			}
		}
	}
}

func handleHostsKey(m *setupModel, k wkey) {
	switch k {
	case keyUp:
		m.moveCursor(-1)
	case keyDown:
		m.moveCursor(1)
	case keySpace:
		m.toggleAtCursor()
	case keyAll:
		m.selectAll()
	case keyDetected:
		m.selectDetected()
	case keySelNone:
		m.selectNone()
	case keyEnter:
		m.advance()
	}
}

func handleOptionsKey(m *setupModel, k wkey) {
	switch k {
	case keyUp:
		m.moveCursor(-1)
	case keyDown:
		m.moveCursor(1)
	case keySpace:
		m.toggleAtCursor()
	case keyEnter:
		m.advance()
	case keyBack:
		m.back()
	}
}

// handleConfirmKey applies the plan on Enter (advancing to scrDone) or
// steps back on b/Esc.
func handleConfirmKey(m *setupModel, k wkey, cwd string) {
	switch k {
	case keyEnter:
		applySetup(m, cwd)
		m.screen = scrDone
		m.cursor = 0
	case keyBack:
		m.back()
	}
}

// applySetup performs the filesystem writes for the chosen targets and
// options, recording one applyResult per action. Installer chatter is
// discarded — the wizard renders its own summary.
func applySetup(m *setupModel, cwd string) {
	m.results = m.results[:0]
	for _, t := range m.selectedTargets() {
		plan, err := pinit.Plan(t, cwd, m.optGlobal)
		if err != nil {
			m.results = append(m.results, applyResult{name: t.Name, ok: false,
				label: fmt.Sprintf("%s — %v", displaySetupName(t), err)})
			continue
		}
		if werr := pinit.WriteFileEnsuringDir(plan.Path, plan.Updated); werr != nil {
			m.results = append(m.results, applyResult{name: t.Name, ok: false,
				label: fmt.Sprintf("%s — write failed: %v", displaySetupName(t), werr)})
			continue
		}
		m.results = append(m.results, applyResult{name: t.Name, ok: true,
			label: fmt.Sprintf("%s — %s %s", displaySetupName(t), plan.Action, condenseHome(plan.Path))})
	}

	if m.selected["claude"] && m.optHook {
		if err := installClaudeHook(io.Discard, cwd, false); err != nil {
			m.results = append(m.results, applyResult{name: "hook", ok: false,
				label: fmt.Sprintf("Claude PreToolUse hook — %v", err)})
		} else {
			m.results = append(m.results, applyResult{name: "hook", ok: true,
				label: "Claude PreToolUse hook — installed (.claude/settings.json)"})
		}
	}
	if m.optGitHooks {
		if err := installGitHooks(io.Discard, cwd, false, false); err != nil {
			m.results = append(m.results, applyResult{name: "githooks", ok: false,
				label: fmt.Sprintf("Git hooks — %v", err)})
		} else {
			m.results = append(m.results, applyResult{name: "githooks", ok: true,
				label: "Git hooks — installed (.git/hooks/)"})
		}
	}
}

// ── rendering ───────────────────────────────────────────────────────

// setupOut is where the wizard paints. Indirected so loop tests can
// redirect it to io.Discard instead of clearing the test terminal.
var setupOut io.Writer = os.Stdout

func paint(lines []string) {
	fmt.Fprint(setupOut, ansiHome)
	fmt.Fprint(setupOut, strings.Join(lines, "\r\n"))
}

func renderScreen(m *setupModel, cwd string) []string {
	switch m.screen {
	case scrHosts:
		return renderHosts(m)
	case scrOptions:
		return renderOptions(m)
	case scrConfirm:
		return renderConfirm(m, cwd)
	default:
		return renderDone(m)
	}
}

func header(title string) []string {
	return []string{"", "  " + tint(ansiAccent+ansiBold, "pincher setup") + " — " + title, ""}
}

func footer(hint string) []string {
	return []string{"", "  " + ansiDim + hint + ansiReset, ""}
}

func renderHosts(m *setupModel) []string {
	lines := header("wire up your editors & agents")
	lines = append(lines, "  "+ansiDim+"Detected hosts are pre-checked. Toggle anything you want."+ansiReset, "")
	for i, t := range m.targets {
		cursor := "  "
		if i == m.cursor {
			cursor = tint(ansiAccent+ansiBold, "› ")
		}
		box := tint(ansiDim, "[ ]")
		if m.selected[t.Name] {
			box = tint(ansiGreen, "[x]")
		}
		name := fmt.Sprintf("%-20s", displaySetupName(t))
		if i == m.cursor {
			name = tint(ansiBold, name)
		}
		suffix := ""
		if m.detected[t.Name] {
			suffix = tint(ansiGreen, "  ·detected")
		}
		lines = append(lines, cursor+box+"  "+name+" "+tint(ansiDim, t.Describe)+suffix)
	}
	if !m.anySelected() {
		lines = append(lines, "", "  "+ansiDim+"(select at least one host to continue)"+ansiReset)
	}
	return append(lines, footer("↑/↓ move · space toggle · a all · d detected · n none · enter continue · q quit")...)
}

func renderOptions(m *setupModel) []string {
	lines := header("install options")
	keys := m.optionKeys()
	for i, key := range keys {
		cursor := "  "
		if i == m.cursor {
			cursor = tint(ansiAccent+ansiBold, "› ")
		}
		box := tint(ansiDim, "[ ]")
		if m.optionValue(key) {
			box = tint(ansiGreen, "[x]")
		}
		label := optionLabel(key)
		if i == m.cursor {
			label = tint(ansiBold, label)
		}
		lines = append(lines, cursor+box+"  "+label)
	}
	return append(lines, footer("↑/↓ move · space toggle · enter continue · b back · q quit")...)
}

func renderConfirm(m *setupModel, cwd string) []string {
	lines := header("review")
	lines = append(lines, "  "+ansiDim+"These files will be written:"+ansiReset, "")
	bullet := tint(ansiAccent, "•")
	for _, t := range m.selectedTargets() {
		plan, err := pinit.Plan(t, cwd, m.optGlobal)
		if err != nil {
			lines = append(lines, fmt.Sprintf("  %s %-20s %v", tint(ansiRed, "!"), displaySetupName(t), err))
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s %-20s %s %s", bullet, displaySetupName(t),
			pinit.PresentTenseAction(plan.Action), condenseHome(plan.Path)))
	}
	if m.selected["claude"] && m.optHook {
		lines = append(lines, "  "+bullet+fmt.Sprintf(" %-20s install .claude/settings.json PreToolUse hook", "Claude hook"))
	}
	if m.optGitHooks {
		lines = append(lines, "  "+bullet+fmt.Sprintf(" %-20s install .git/hooks reindex hooks", "Git hooks"))
	}
	return append(lines, footer("enter apply · b back · q cancel")...)
}

func renderDone(m *setupModel) []string {
	lines := header("done")
	for _, r := range m.results {
		mark := tint(ansiGreen, "✓")
		if !r.ok {
			mark = tint(ansiRed, "✗")
		}
		lines = append(lines, "  "+mark+" "+r.label)
	}
	lines = append(lines, "", "  "+ansiDim+"Next: run `pincher index` to build the symbol graph, then connect your MCP client."+ansiReset)
	return append(lines, footer("i index this project now · enter finish")...)
}

// ── key input ───────────────────────────────────────────────────────

// readKey reads one keystroke from raw-mode stdin and decodes it.
func readKey() wkey {
	var buf [8]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil || n == 0 {
		return keyQuit // treat a closed stdin as quit rather than spinning
	}
	return decodeKey(buf[:n])
}

// decodeKey maps a raw keystroke byte slice to a wkey. Split out from
// readKey so the decode table is unit-testable without a TTY.
func decodeKey(b []byte) wkey {
	if len(b) == 0 {
		return keyQuit
	}
	// CSI arrow sequences: ESC [ A/B.
	if len(b) >= 3 && b[0] == 0x1b && b[1] == '[' {
		switch b[2] {
		case 'A':
			return keyUp
		case 'B':
			return keyDown
		}
		return keyNone
	}
	switch b[0] {
	case '\r', '\n':
		return keyEnter
	case ' ':
		return keySpace
	case 0x03: // Ctrl-C
		return keyQuit
	case 0x1b: // bare ESC
		return keyBack
	case 'q', 'Q':
		return keyQuit
	case 'a', 'A':
		return keyAll
	case 'd', 'D':
		return keyDetected
	case 'n', 'N':
		return keySelNone
	case 'b', 'B':
		return keyBack
	case 'i', 'I':
		return keyIndex
	}
	return keyNone
}

// ── helpers ─────────────────────────────────────────────────────────

// displaySetupName is the human-facing target name for the wizard.
func displaySetupName(t pinit.Target) string { return t.Name }

func optionLabel(key string) string {
	switch key {
	case optGlobalKey:
		return "Install rules globally where the host supports it"
	case optHookKey:
		return "Claude PreToolUse hook (redirects Read/Grep → pincher)"
	case optGitHooksKey:
		return "Git hooks — reindex on branch switch / rebase"
	}
	return key
}

// condenseHome shortens an absolute path under the user's home to ~/…
// so the confirm screen stays narrow.
func condenseHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// isGitRepo reports whether cwd is inside a git working tree (a .git
// entry at the root). Gates the git-hooks option.
func isGitRepo(cwd string) bool {
	_, err := os.Stat(filepath.Join(cwd, ".git"))
	return err == nil
}

// printSetupSummary echoes the apply results to the cooked terminal so
// they survive after the alt-screen-free wizard clears.
func printSetupSummary(m *setupModel) {
	fmt.Fprintln(setupOut)
	fmt.Fprintln(setupOut, "pincher setup — summary:")
	for _, r := range m.results {
		mark := "OK  "
		if !r.ok {
			mark = "FAIL"
		}
		fmt.Fprintf(setupOut, "  [%s] %s\n", mark, r.label)
	}
}

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/kwad77/pincher/internal/db"
	pinit "github.com/kwad77/pincher/internal/init"
)

// runInitCLI implements `pincher init [--global] [--dry-run] [--force]`.
//
// Writes (or replaces, in place) a pincher usage policy block in
// either the project-local CLAUDE.md (default) or the global
// ~/.claude/CLAUDE.md (when --global is set). The block is wrapped
// in `<!-- pincher:start --> ... <!-- pincher:end -->` markers so a
// future `pincher init` run can update it without leaving stale
// duplicates.
//
// The pure planning + merge logic lives in internal/init (#253);
// this function is the CLI orchestration layer.

// initPickerShortlist is the curated set offered when `pincher init`
// can't auto-detect the host and stdin is interactive. Ordered by
// rough popularity; the "other" menu entry expands to AllTargets.
var initPickerShortlist = []struct{ target, label string }{
	{"claude", "Claude Code"},
	{"codex", "OpenAI Codex"},
	{"cursor", "Cursor"},
	{"vscode", "VS Code (Copilot)"},
	{"gemini", "Gemini CLI"},
}

// autoResolveInitTarget picks the init target when `pincher init` ran
// with no --target. It uses the shared host-aware resolver; when that
// is inconclusive it asks the user interactively (if stdin is a TTY)
// and otherwise refuses with exit 1 — #1862, silently defaulting to
// claude gave a Codex user a CLAUDE.md + .claude/ hook they never
// asked for.
func autoResolveInitTarget(cwd string, out io.Writer) string {
	res := pinit.AutoResolveInitTarget(cwd)
	if res.Decided {
		fmt.Fprintf(out, "pincher init: no --target given — %s. Pass --target to override.\n", res.Reason)
		return res.Target
	}
	// Inconclusive. A human at a terminal gets a picker; a scripted /
	// agent invocation (no TTY) gets a clear refusal so it can re-run
	// with an explicit --target.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if picked, ok := promptInitTarget(out, os.Stdin); ok {
			return picked
		}
	}
	fmt.Fprintln(os.Stderr, "pincher init: could not determine which agent/editor to configure.")
	fmt.Fprintln(os.Stderr, "  No host env signal (e.g. CLAUDECODE) and no editor marker files were found.")
	fmt.Fprintf(os.Stderr, "  Pass --target=NAME explicitly — one of: %s\n", strings.Join(pinit.TargetNames(), ", "))
	fmt.Fprintln(os.Stderr, "  Or --target=detect to scan, --target=all for every target.")
	os.Exit(1)
	return "" // unreachable — os.Exit above
}

// promptInitTarget asks the user to choose a target when init couldn't
// auto-detect one and stdin is interactive. Returns ("", false) on EOF
// or an unrecognized choice, in which case the caller refuses.
func promptInitTarget(out io.Writer, in io.Reader) (string, bool) {
	sc := bufio.NewScanner(in)
	n := len(initPickerShortlist)
	fmt.Fprintln(out, "pincher init: couldn't auto-detect your agent/editor — which are you setting up?")
	for i, o := range initPickerShortlist {
		fmt.Fprintf(out, "  %d. %-8s %s\n", i+1, o.target, o.label)
	}
	fmt.Fprintf(out, "  %d. detect   scan marker files, configure every match\n", n+1)
	fmt.Fprintf(out, "  %d. other    pick from the full target list\n", n+2)
	fmt.Fprintf(out, "Enter 1-%d (or a target name): ", n+2)
	if !sc.Scan() {
		return "", false
	}

	switch choice := strings.TrimSpace(sc.Text()); {
	case isMenuIndex(choice, 1, n):
		idx, _ := strconv.Atoi(choice)
		return initPickerShortlist[idx-1].target, true
	case choice == strconv.Itoa(n+1) || choice == "detect":
		return "detect", true
	case choice == strconv.Itoa(n+2) || choice == "other":
		return promptInitTargetFull(out, sc)
	default:
		// A literal target name typed instead of a menu number.
		if _, ok := pinit.FindTarget(choice); ok {
			return choice, true
		}
		fmt.Fprintf(out, "pincher init: %q is not a valid choice.\n", choice)
		return "", false
	}
}

// promptInitTargetFull shows the complete AllTargets list (the "other"
// branch of the shortlist menu) and reads one selection.
func promptInitTargetFull(out io.Writer, sc *bufio.Scanner) (string, bool) {
	for i, t := range pinit.AllTargets {
		fmt.Fprintf(out, "  %2d. %-16s %s\n", i+1, t.Name, t.Describe)
	}
	fmt.Fprintf(out, "Enter 1-%d (or a target name): ", len(pinit.AllTargets))
	if !sc.Scan() {
		return "", false
	}
	choice := strings.TrimSpace(sc.Text())
	if isMenuIndex(choice, 1, len(pinit.AllTargets)) {
		idx, _ := strconv.Atoi(choice)
		return pinit.AllTargets[idx-1].Name, true
	}
	if _, ok := pinit.FindTarget(choice); ok {
		return choice, true
	}
	fmt.Fprintf(out, "pincher init: %q is not a valid choice.\n", choice)
	return "", false
}

// isMenuIndex reports whether s is an integer in [lo, hi].
func isMenuIndex(s string, lo, hi int) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= lo && n <= hi
}

func runInitCLI(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	global := fs.Bool("global", false, "Write the global rules file (target-dependent; e.g. ~/.claude/CLAUDE.md for claude)")
	dryRun := fs.Bool("dry-run", false, "Print what would be written; do not modify any file")
	force := fs.Bool("force", false, "Overwrite the marker block without prompting (default behavior anyway, kept for explicit scripted use)")
	dataDir := fs.String("data-dir", "", "Override data directory (used to discover the running HTTP dashboard URL)")
	targetFlag := fs.String("target", "", "Editor/agent target: "+strings.Join(pinit.TargetNames(), ", ")+". Default: auto-detect the host pincher is running under (env signal) then editor marker files; refuses rather than guessing when neither is conclusive.")
	noHook := fs.Bool("no-hook", false, "(claude target only) Skip writing the .claude/settings.json PreToolUse hook. Default false — the hook is what closes the Read/Grep → pincher gap at runtime.")
	gitHooks := fs.Bool("git-hooks", false, "Install post-checkout / post-merge / post-rewrite git hooks into .git/hooks so branch switches and rebases trigger an eager reindex (#1261). Pincher-managed hooks carry a marker comment; pre-existing non-pincher hooks are skipped unless --force is set.")
	quiet := fs.Bool("quiet", false, "Suppress the per-language extraction-tier profile printed after the wiring step (#631). The wiring itself still runs.")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pincher init [--target=NAME] [--global] [--dry-run] [--force]")
		fmt.Fprintln(os.Stderr, "  Seed a pincher usage policy file for an editor or agent (idempotent; replace-in-place via marker comments).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Targets:")
		for _, t := range pinit.AllTargets {
			fmt.Fprintf(os.Stderr, "    %-14s %s\n", t.Name, t.Describe)
		}
		fmt.Fprintln(os.Stderr, "    detect         Pick every target whose marker file exists under cwd")
		fmt.Fprintln(os.Stderr, "    all            Write every project-scoped target")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	out := os.Stdout
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher init: cwd: %v\n", err)
		os.Exit(1)
	}
	// #1862: bare `pincher init` (no --target) must NOT silently default
	// to claude — a Codex user running it got CLAUDE.md + .claude/. When
	// --target is omitted, resolve it host-aware: env signal → marker
	// files → refuse rather than guess.
	if *targetFlag == "" {
		*targetFlag = autoResolveInitTarget(cwd, out)
	}
	targets, err := pinit.ResolveTargets(*targetFlag, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher init: %v\n", err)
		os.Exit(1)
	}

	for _, t := range targets {
		if err := runInitTarget(out, t, cwd, *global, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "pincher init: %v\n", err)
			os.Exit(1)
		}
		// #627: when target=claude (and we're not running a global
		// install — hooks are project-scoped), wire the PreToolUse hook
		// so that Read/Grep on indexed files redirects to pincher
		// equivalents at runtime. Without this, the CLAUDE.md policy
		// is the only nudge — and instruction-layer nudges plateau.
		if t.Name == "claude" && !*global && !*noHook {
			if err := installClaudeHook(out, cwd, *dryRun); err != nil {
				fmt.Fprintf(os.Stderr, "pincher init: hook install: %v\n", err)
				os.Exit(1)
			}
		}
	}

	// #1261: git hooks are independent of target — install once when
	// requested, regardless of which editor/agent rules files were
	// written. --global skips them since hooks live in .git/hooks/
	// (per-repo, not per-user).
	if *gitHooks && !*global {
		if err := installGitHooks(out, cwd, *dryRun, *force); err != nil {
			fmt.Fprintf(os.Stderr, "pincher init: git-hooks install: %v\n", err)
			os.Exit(1)
		}
	}

	if !*dryRun {
		// #631: print the per-language extraction-tier profile so the
		// user sees "Ruby is regex-tier, Scala is stub-tier" before
		// they run their first session and conclude pincher doesn't
		// work. --quiet suppresses for CI/scripted installs. Profile
		// failures are non-fatal — install already succeeded.
		if !*quiet {
			if profile, err := pinit.ProfileDir(cwd); err == nil {
				pinit.PrintProfile(out, profile)
			}
		}
		printNextSteps(out, *dataDir)
	}
}

// runInitTarget writes (or dry-runs) a single target. global is the
// user's --global flag; for targets that don't support it, the
// underlying Plan call silently ignores rather than errors so that
// --target=all keeps working with --global set.
//
// Dry-run action grammar (#803) is handled by pinit.PresentTenseAction,
// shared with the MCP handler's JSON `action` field (#849).
func runInitTarget(out io.Writer, t pinit.Target, cwd string, global, dryRun bool) error {
	plan, err := pinit.Plan(t, cwd, global)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprintf(out, "pincher init [%s]: would %s %s\n\n", plan.Target, pinit.PresentTenseAction(plan.Action), plan.Path)
		fmt.Fprintln(out, "--- new file content ---")
		fmt.Fprintln(out, plan.Updated)
		return nil
	}

	if err := pinit.WriteFileEnsuringDir(plan.Path, plan.Updated); err != nil {
		return fmt.Errorf("[%s] write %s: %w", plan.Target, plan.Path, err)
	}
	fmt.Fprintf(out, "pincher init [%s]: %s %s\n", plan.Target, plan.Action, plan.Path)
	return nil
}

// printNextSteps emits a guide-style recipe + the URL of any running
// HTTP dashboard. Failures are non-fatal — the init succeeded by the
// time we get here, and a missing data dir or empty sessions table
// just means we have nothing to add to the recipe.
func printNextSteps(out io.Writer, dataDirOverride string) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  1. Run `pincher index` from this directory to build the symbol graph.")
	fmt.Fprintln(out, "  2. Connect your MCP client (Claude Code, Cursor, etc.) to `pincher`.")
	fmt.Fprintln(out, "  3. Or open the dashboard: `pincher web`")

	dir := dataDirOverride
	if dir == "" {
		var err error
		dir, err = db.DataDir()
		if err != nil {
			return
		}
	}
	store, err := db.Open(dir)
	if err != nil {
		return
	}
	defer store.Close()

	if base, _, ok := findLiveHTTPServer(store); ok {
		fmt.Fprintf(out, "\nLive dashboard: %s\n", dashboardURL(base))
	}
}

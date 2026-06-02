package server

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #1509 v0.83 (closes the drift surfaced in #672 workstream 4):
// assert every subcommand advertised in `pincher --help`'s banner
// (cmd/pinch/main.go printHelpBanner) has a dedicated
// `### `pincher <name>`` section in docs/reference/cli.md.
//
// This catches the "is this supported?" gap where `pincher --help`
// advertises a command but the reference entry point forces users back
// to `pincher <cmd> --help` for the first explanation.
//
// Complementary to TestReferenceMD_EveryRegisteredToolMentioned which
// pins the MCP-tool side. This test pins the CLI-subcommand side.
//
// Source of truth is cmd/pinch/main.go's printHelpBanner. Adding a
// subcommand to that banner requires adding a section to REFERENCE.md;
// this test enforces lockstep so the docs entry point can never
// silently drift from the advertised CLI surface again.

func TestReferenceMD_EveryCLISubcommandHasSection(t *testing.T) {
	t.Parallel()

	refBytes, err := os.ReadFile("../../docs/reference/cli.md")
	if err != nil {
		t.Fatalf("read docs/reference/cli.md: %v", err)
	}
	ref := string(refBytes)

	// We look for the precise `### `pincher <name>`` heading shape,
	// not just a backticked mention — many subcommands are name-dropped
	// in prose without having their own section. The h3 + backticks
	// is the canonical "this subcommand is documented" marker.
	for _, sub := range advertisedCLISubcommands(t) {
		heading := "### `pincher " + sub + "`"
		if !strings.Contains(ref, heading) {
			t.Errorf("CLI subcommand %q has no dedicated `### \\`pincher %s\\`` section in docs/reference/cli.md — add one (see existing sections for the standard shape) or remove the subcommand from cmd/pinch/main.go printHelpBanner if intentionally hidden",
				sub, sub)
		}
	}
}

// TestReferenceMD_NoOrphanCLISection is the inverse gate: every pincher
// subcommand heading in docs/reference/cli.md must correspond to a subcommand on
// the expected list. Catches the drift direction where a subcommand is
// removed from the help banner but the docs section sticks around as a
// confusing residue.
//
// `pincher init --git-hooks` is a sub-mode of `init` and is exempt
// because its heading shape does not match the regex's `\w+` group.
func TestReferenceMD_NoOrphanCLISection(t *testing.T) {
	t.Parallel()

	refBytes, err := os.ReadFile("../../docs/reference/cli.md")
	if err != nil {
		t.Fatalf("read docs/reference/cli.md: %v", err)
	}
	ref := string(refBytes)

	subs := advertisedCLISubcommands(t)
	known := make(map[string]bool, len(subs))
	for _, s := range subs {
		known[s] = true
	}

	for _, m := range cliSectionHeadingRE.FindAllStringSubmatch(ref, -1) {
		name := m[1]
		if !known[name] {
			t.Errorf("docs/reference/cli.md has `### \\`pincher %s\\`` but %q is not advertised by printHelpBanner — either add it to the help banner, or remove the orphan section",
				name, name)
		}
	}
}

// Match pincher single-token headings, optionally followed by
// a tracking suffix such as `(#1399)`. The single-token shape excludes
// sub-modes like `pincher init --git-hooks`.
var cliSectionHeadingRE = regexp.MustCompile("(?m)^### `pincher ([a-z][a-z\\-]*)`(?:\\s+\\([^)]*\\))?\\s*$")

func TestReferenceMD_CLISectionHeadingREAllowsIssueSuffix(t *testing.T) {
	t.Parallel()

	sample := "### `pincher verify` (#1399)\n### `pincher hook-stats` (#662)\n### `pincher init --git-hooks`\n"
	var got []string
	for _, m := range cliSectionHeadingRE.FindAllStringSubmatch(sample, -1) {
		got = append(got, m[1])
	}
	want := []string{"verify", "hook-stats"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parsed headings = %v, want %v", got, want)
	}
}

func TestDocsDoNotMentionRemovedHealthOrSearchCLIs(t *testing.T) {
	t.Parallel()

	paths := []string{
		"../../docs/troubleshooting.md",
		"../../docs/deployment/observability.md",
		"../../docs/reference/cli.md",
		"../../docs/migration/v0.4-to-v1.0.md",
		"../../cmd/pinch/rebuild_fts.go",
	}
	removed := map[string]*regexp.Regexp{
		"pincher health": regexp.MustCompile("pincher health(?:\\s|`|$)"),
		"pincher search": regexp.MustCompile("pincher search(?:\\s|`|$)"),
		"pincher query":  regexp.MustCompile("pincher query(?:\\s|`|$)"),
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for label, re := range removed {
			if loc := re.FindIndex(body); loc != nil {
				start := max(0, loc[0]-80)
				end := min(len(body), loc[1]+80)
				t.Fatalf("%s references removed CLI command %q near:\n%s", path, label, body[start:end])
			}
		}
	}
}

func advertisedCLISubcommands(t *testing.T) []string {
	t.Helper()

	srcBytes, err := os.ReadFile("../../cmd/pinch/main.go")
	if err != nil {
		t.Fatalf("read cmd/pinch/main.go: %v", err)
	}
	src := string(srcBytes)

	start := strings.Index(src, "func printHelpBanner")
	if start < 0 {
		t.Fatal("cmd/pinch/main.go: printHelpBanner not found")
	}
	end := strings.Index(src[start:], "func printGroupedFlags")
	if end < 0 {
		t.Fatal("cmd/pinch/main.go: printGroupedFlags not found after printHelpBanner")
	}
	body := src[start : start+end]

	re := regexp.MustCompile(`fmt\.Fprintln\(out, "  pincher ([a-z][a-z-]*)(?:[ "])`)
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		seen[m[1]] = true
	}
	if len(seen) == 0 {
		t.Fatal("no CLI subcommands parsed from printHelpBanner")
	}

	out := make([]string, 0, len(seen))
	for sub := range seen {
		out = append(out, sub)
	}
	sort.Strings(out)
	return out
}

func TestReferenceMD_SelfTestDocumentsJSONMode(t *testing.T) {
	t.Parallel()

	refBytes, err := os.ReadFile("../../docs/reference/cli.md")
	if err != nil {
		t.Fatalf("read docs/reference/cli.md: %v", err)
	}
	ref := string(refBytes)

	for _, want := range []string{
		"pincher self-test --json",
		"`--json` writes a single structured report to stdout",
	} {
		if !strings.Contains(ref, want) {
			t.Fatalf("docs/reference/cli.md self-test section does not document %q", want)
		}
	}
}

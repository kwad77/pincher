// SPDX-License-Identifier: MIT

package init

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shippedSkills is the contract list — every skill the binary must
// ship. Adding a skill under plugin/skills/ requires adding it to the
// embed directive (plugin/skills/embed.go) AND here, so a forgotten
// embed fails loudly instead of silently shipping four of five.
var shippedSkills = []string{
	"pincher-debug",
	"pincher-loop",
	"pincher-onboard",
	"pincher-review",
	"pincher-steward",
}

func TestPlanSkillsFreshInstall(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	plans, err := PlanSkills(dest)
	if err != nil {
		t.Fatalf("PlanSkills: %v", err)
	}
	if len(plans) != len(shippedSkills) {
		t.Fatalf("got %d plans, want %d (%v)", len(plans), len(shippedSkills), plans)
	}
	for i, p := range plans {
		if p.Name != shippedSkills[i] {
			t.Errorf("plan[%d].Name = %q, want %q (sorted order)", i, p.Name, shippedSkills[i])
		}
		if p.Action != "install" {
			t.Errorf("[%s] fresh dest should plan install, got %q", p.Name, p.Action)
		}
		if p.EmbeddedVersion == "" {
			t.Errorf("[%s] shipped SKILL.md must carry a version: frontmatter line", p.Name)
		}
		if p.InstalledVersion != "" {
			t.Errorf("[%s] nothing installed yet, got InstalledVersion %q", p.Name, p.InstalledVersion)
		}
		hasSkillMD := false
		for _, f := range p.Files {
			if f.RelPath == "SKILL.md" {
				hasSkillMD = true
			}
		}
		if !hasSkillMD {
			t.Errorf("[%s] plan lacks SKILL.md (files: %v)", p.Name, p.Files)
		}
	}
}

// The flagship ships its references/ directory; the walk must pick up
// nested files, not just the top-level SKILL.md.
func TestPlanSkillsIncludesReferences(t *testing.T) {
	t.Parallel()
	plans, err := PlanSkills(t.TempDir())
	if err != nil {
		t.Fatalf("PlanSkills: %v", err)
	}
	for _, p := range plans {
		if p.Name != "pincher-loop" {
			continue
		}
		refs := 0
		for _, f := range p.Files {
			if strings.HasPrefix(f.RelPath, "references/") {
				refs++
			}
		}
		if refs < 4 {
			t.Errorf("pincher-loop should ship >=4 references/*.md, got %d (%v)", refs, p.Files)
		}
		return
	}
	t.Fatal("pincher-loop not in plans")
}

func TestInstallSkillsRoundTrip(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	plans, err := PlanSkills(dest)
	if err != nil {
		t.Fatalf("PlanSkills: %v", err)
	}
	n, err := InstallSkills(plans)
	if err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	if n != len(shippedSkills) {
		t.Fatalf("installed %d skills, want %d", n, len(shippedSkills))
	}

	// Every planned file landed byte-identical.
	for _, p := range plans {
		for _, f := range p.Files {
			got, err := os.ReadFile(filepath.Join(dest, p.Name, filepath.FromSlash(f.RelPath)))
			if err != nil {
				t.Fatalf("[%s] %s not written: %v", p.Name, f.RelPath, err)
			}
			if string(got) != string(f.Content) {
				t.Errorf("[%s] %s drifted on write", p.Name, f.RelPath)
			}
		}
	}

	// Re-plan: everything unchanged, and a re-install is a no-op.
	plans2, err := PlanSkills(dest)
	if err != nil {
		t.Fatalf("re-PlanSkills: %v", err)
	}
	for _, p := range plans2 {
		if p.Action != "unchanged" {
			t.Errorf("[%s] after install want unchanged, got %q", p.Name, p.Action)
		}
		if p.InstalledVersion != p.EmbeddedVersion {
			t.Errorf("[%s] installed version %q != embedded %q", p.Name, p.InstalledVersion, p.EmbeddedVersion)
		}
	}
	if n2, err := InstallSkills(plans2); err != nil || n2 != 0 {
		t.Errorf("re-install should be a no-op, got (n=%d, err=%v)", n2, err)
	}
}

// An installed copy with an OLDER version (or same version with
// drifted content) is updated in place — the shipped copy is canonical
// at or above the installed version.
func TestPlanSkillsUpdatesOlderLocal(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	old := "---\nname: pincher-loop\nversion: 0.0.1\n---\n\n# stale\n"
	if err := WriteFileEnsuringDir(filepath.Join(dest, "pincher-loop", "SKILL.md"), old); err != nil {
		t.Fatal(err)
	}

	plans, err := PlanSkills(dest)
	if err != nil {
		t.Fatalf("PlanSkills: %v", err)
	}
	for _, p := range plans {
		if p.Name != "pincher-loop" {
			continue
		}
		if p.Action != "update" {
			t.Fatalf("older local copy should plan update, got %q", p.Action)
		}
		if p.InstalledVersion != "0.0.1" {
			t.Fatalf("InstalledVersion = %q, want 0.0.1", p.InstalledVersion)
		}
	}

	if _, err := InstallSkills(plans); err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	got := ReadFileIfExists(filepath.Join(dest, "pincher-loop", "SKILL.md"))
	if strings.Contains(got, "# stale") {
		t.Error("older local SKILL.md should have been replaced by the shipped copy")
	}
}

// The refusal gate: an installed SKILL.md declaring a NEWER version
// than the shipped one is never overwritten — overwriting would be a
// silent downgrade of a copy the user (or a newer pincher) owns.
func TestPlanSkillsRefusesNewerLocal(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	newer := "---\nname: pincher-loop\nversion: 99.0.0\n---\n\n# user's future copy\n"
	path := filepath.Join(dest, "pincher-loop", "SKILL.md")
	if err := WriteFileEnsuringDir(path, newer); err != nil {
		t.Fatal(err)
	}

	plans, err := PlanSkills(dest)
	if err != nil {
		t.Fatalf("PlanSkills: %v", err)
	}
	for _, p := range plans {
		if p.Name == "pincher-loop" && p.Action != "skip_newer_local" {
			t.Fatalf("newer local copy must be refused, got action %q", p.Action)
		}
	}

	if _, err := InstallSkills(plans); err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	if got := ReadFileIfExists(path); got != newer {
		t.Error("InstallSkills must not touch a skill planned skip_newer_local")
	}
}

func TestParseSkillVersion(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, content, want string }{
		{"frontmatter", "---\nname: x\nversion: 0.3.0\n---\nbody", "0.3.0"},
		{"capitalised", "---\nVersion: 1.2\n---\n", "1.2"},
		{"quoted", "---\nversion: \"2.0.1\"\n---\n", "2.0.1"},
		{"missing", "---\nname: x\n---\nversion-free body", ""},
		// A version: line after the frontmatter closed must not count.
		{"body line ignored", "---\nname: x\n---\nversion: 9.9.9\n", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := ParseSkillVersion(c.content); got != c.want {
			t.Errorf("%s: ParseSkillVersion = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCompareSkillVersions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"0.3.0", "0.3.0", 0},
		{"0.3.0", "0.10.0", -1}, // numeric, not lexicographic
		{"1.0.0", "0.9.9", 1},
		{"", "0.0.1", -1}, // versionless is older than anything
		{"0.0.1", "", 1},
		{"", "", 0},
		{"1.0", "1.0.1", -1},
	}
	for _, c := range cases {
		if got := CompareSkillVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareSkillVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// claude-skills is enumerated for --help but is NOT a registry Target:
// ResolveTargets must fail with directions (not "unknown --target"),
// and the registry/detect/all paths must never include it implicitly.
func TestSkillsTargetNameOutsideRegistry(t *testing.T) {
	t.Parallel()

	names := TargetNames()
	found := false
	for _, n := range names {
		if n == SkillsTargetName {
			found = true
		}
	}
	if !found {
		t.Errorf("TargetNames() should enumerate %q for help text: %v", SkillsTargetName, names)
	}

	if _, ok := FindTarget(SkillsTargetName); ok {
		t.Errorf("%q must not be a registry Target (its Plan shape doesn't fit)", SkillsTargetName)
	}

	_, err := ResolveTargets(SkillsTargetName, t.TempDir())
	if err == nil {
		t.Fatalf("ResolveTargets(%q) should refuse", SkillsTargetName)
	}
	if !strings.Contains(err.Error(), "skills installer") {
		t.Errorf("refusal should point at the skills installer, got: %v", err)
	}
}

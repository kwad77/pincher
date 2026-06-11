// SPDX-License-Identifier: MIT

package init

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kwad77/pincher/plugin/skills"
)

// SkillsTargetName is the `pincher init --target` value that installs
// the shipped methodology skills (plugin/skills/*) into the user's
// ~/.claude/skills/ directory.
//
// claude-skills is NOT a registry Target: its path is always global
// (it escapes any project root, like continue's), and it writes a
// tree of files per skill rather than one marker-block file — the
// Target/Plan/WriteFn machinery doesn't fit. The CLI special-cases
// the name (and appends it to `--target=all`); the MCP handler
// refuses it the same way it refuses continue.
const SkillsTargetName = "claude-skills"

// SkillFile is one file inside a skill, with its embedded content.
type SkillFile struct {
	// RelPath is the path relative to the skill's directory, e.g.
	// "SKILL.md" or "references/gates.md". Always slash-separated.
	RelPath string
	Content []byte
}

// SkillPlan is the pure result of comparing one embedded skill
// against the user's installed copy — what would happen on install,
// before any disk write. Mirrors TargetPlan's plan-then-write split
// so CLI and any future MCP surface share one source of truth.
type SkillPlan struct {
	// Name is the skill directory name, e.g. "pincher-loop".
	Name string
	// Dir is the absolute destination directory the skill installs to.
	Dir string
	// EmbeddedVersion is the `version:` value parsed from the shipped
	// SKILL.md frontmatter.
	EmbeddedVersion string
	// InstalledVersion is the `version:` parsed from the user's
	// installed SKILL.md ("" when no copy is installed or the copy
	// has no version line).
	InstalledVersion string
	// Action is one of:
	//   "install"          — no installed copy; all files would be written
	//   "update"           — installed copy is older (or same version
	//                        with drifted content); files would be replaced
	//   "unchanged"        — installed copy is byte-identical
	//   "skip_newer_local" — the installed SKILL.md declares a NEWER
	//                        version than the shipped one; refused, never
	//                        overwritten (the user is ahead of the binary)
	Action string
	// Files is every file the skill ships, in stable sorted order.
	Files []SkillFile
}

// DefaultSkillsDir returns ~/.claude/skills — the per-user Claude Code
// skills directory claude-skills installs into.
func DefaultSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// PlanSkills compares every embedded skill against destRoot and
// returns one SkillPlan per skill, sorted by name. Pure apart from
// reading destRoot; the write is InstallSkills.
func PlanSkills(destRoot string) ([]SkillPlan, error) {
	entries, err := fs.ReadDir(skills.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded skills: %w", err)
	}

	var plans []SkillPlan
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		plan, err := planOneSkill(destRoot, e.Name())
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Name < plans[j].Name })
	return plans, nil
}

func planOneSkill(destRoot, name string) (SkillPlan, error) {
	plan := SkillPlan{
		Name: name,
		Dir:  filepath.Join(destRoot, name),
	}

	err := fs.WalkDir(skills.FS, name, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(skills.FS, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		rel := strings.TrimPrefix(path, name+"/")
		plan.Files = append(plan.Files, SkillFile{RelPath: rel, Content: content})
		return nil
	})
	if err != nil {
		return SkillPlan{}, err
	}
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].RelPath < plan.Files[j].RelPath })

	for _, f := range plan.Files {
		if f.RelPath == "SKILL.md" {
			plan.EmbeddedVersion = ParseSkillVersion(string(f.Content))
		}
	}

	installed := ReadFileIfExists(filepath.Join(plan.Dir, "SKILL.md"))
	if installed == "" {
		plan.Action = "install"
		return plan, nil
	}
	plan.InstalledVersion = ParseSkillVersion(installed)

	// The overwrite-refusal gate: a user copy declaring a NEWER
	// version than the binary ships is never touched — the user (or a
	// newer pincher) is ahead of this binary, and clobbering their
	// copy with an older skill would be a silent downgrade. To force a
	// downgrade, delete the installed skill directory first.
	if CompareSkillVersions(plan.InstalledVersion, plan.EmbeddedVersion) > 0 {
		plan.Action = "skip_newer_local"
		return plan, nil
	}

	// Same or older version: shipped content is canonical. Distinguish
	// "byte-identical" (unchanged — a no-op the caller may not even
	// surface) from "would replace" (update).
	plan.Action = "unchanged"
	for _, f := range plan.Files {
		existing := ReadFileIfExists(filepath.Join(plan.Dir, filepath.FromSlash(f.RelPath)))
		if existing != string(f.Content) {
			plan.Action = "update"
			break
		}
	}
	return plan, nil
}

// InstallSkills writes every actionable plan ("install" / "update")
// to disk. "unchanged" and "skip_newer_local" plans are left alone.
// Returns the number of skills written.
func InstallSkills(plans []SkillPlan) (int, error) {
	written := 0
	for _, p := range plans {
		if p.Action != "install" && p.Action != "update" {
			continue
		}
		for _, f := range p.Files {
			path := filepath.Join(p.Dir, filepath.FromSlash(f.RelPath))
			if err := WriteFileEnsuringDir(path, string(f.Content)); err != nil {
				return written, fmt.Errorf("[%s] write %s: %w", SkillsTargetName, path, err)
			}
		}
		written++
	}
	return written, nil
}

// ParseSkillVersion extracts the version from a SKILL.md's YAML
// frontmatter — the first `version:` (or `Version:`) line in the
// file's leading frontmatter block (or, defensively, anywhere in the
// first frontmatter-less file). Returns "" when no version line is
// found; "" compares older than any declared version.
func ParseSkillVersion(content string) string {
	inFrontmatter := false
	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "---" {
			if i == 0 {
				inFrontmatter = true
				continue
			}
			if inFrontmatter {
				break // frontmatter closed without a version line
			}
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "version:") {
			v := strings.TrimSpace(trimmed[len("version:"):])
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// CompareSkillVersions compares two dotted version strings
// numerically per segment ("0.10.0" > "0.9.1"). Non-numeric segments
// fall back to string comparison. Empty string is older than any
// non-empty version. Returns -1 / 0 / +1.
func CompareSkillVersions(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var sa, sb string
		if i < len(as) {
			sa = as[i]
		}
		if i < len(bs) {
			sb = bs[i]
		}
		na, errA := strconv.Atoi(sa)
		nb, errB := strconv.Atoi(sb)
		switch {
		case errA == nil && errB == nil:
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
		default:
			if sa != sb {
				if sa < sb {
					return -1
				}
				return 1
			}
		}
	}
	return 0
}

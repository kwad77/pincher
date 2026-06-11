// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"

	pinit "github.com/kwad77/pincher/internal/init"
)

// runInitSkillsDefault is the CLI entry for the claude-skills target:
// plan/install the shipped methodology skills into the default
// ~/.claude/skills directory. write=false (the default — claude-skills
// previews unless --write is passed) only prints the plan.
func runInitSkillsDefault(out io.Writer, write bool) error {
	dest, err := pinit.DefaultSkillsDir()
	if err != nil {
		return fmt.Errorf("[%s] %w", pinit.SkillsTargetName, err)
	}
	return runInitSkills(out, dest, write)
}

// runInitSkills plans the shipped skills against destRoot, prints one
// line per skill, and (when write is set) installs the actionable
// ones. Split from runInitSkillsDefault so tests can point destRoot at
// a temp dir.
//
// Per-skill action vocabulary mirrors internal/init.SkillPlan:
// install / update / unchanged / skip_newer_local — the last is the
// overwrite-refusal gate (the installed SKILL.md declares a newer
// `version:` than the shipped copy) and is never overridden, not even
// by --force; delete the installed skill directory to downgrade.
func runInitSkills(out io.Writer, destRoot string, write bool) error {
	plans, err := pinit.PlanSkills(destRoot)
	if err != nil {
		return fmt.Errorf("[%s] %w", pinit.SkillsTargetName, err)
	}

	actionable := 0
	for _, p := range plans {
		switch p.Action {
		case "unchanged":
			fmt.Fprintf(out, "pincher init [%s]: %s v%s — unchanged at %s\n",
				pinit.SkillsTargetName, p.Name, p.EmbeddedVersion, condenseHome(p.Dir))
		case "skip_newer_local":
			fmt.Fprintf(out, "pincher init [%s]: %s — refused: installed v%s is newer than shipped v%s; delete %s to downgrade\n",
				pinit.SkillsTargetName, p.Name, p.InstalledVersion, p.EmbeddedVersion, condenseHome(p.Dir))
		default: // install / update
			actionable++
			verb := p.Action
			if !write {
				verb = "would " + p.Action
			}
			fmt.Fprintf(out, "pincher init [%s]: %s %s v%s → %s (%d files)\n",
				pinit.SkillsTargetName, verb, p.Name, p.EmbeddedVersion, condenseHome(p.Dir), len(p.Files))
		}
	}

	if !write {
		if actionable > 0 {
			fmt.Fprintf(out, "pincher init [%s]: dry-run (default for this target) — re-run with --write to install\n",
				pinit.SkillsTargetName)
		}
		return nil
	}

	n, err := pinit.InstallSkills(plans)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "pincher init [%s]: installed %d skill(s) under %s\n",
		pinit.SkillsTargetName, n, condenseHome(destRoot))
	return nil
}

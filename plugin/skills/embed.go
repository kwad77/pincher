// SPDX-License-Identifier: MIT

// Package skills embeds the pincher methodology skills that ship with
// the tool. The canonical copies live here, next to the Claude Code
// plugin manifest, so the plugin surface (which loads skills/ natively)
// and the binary (`pincher init --target=claude-skills`, which installs
// them to ~/.claude/skills/) distribute the exact same files.
//
// The embed lives in its own package because go:embed cannot reach
// outside a package's directory — internal/init imports this package
// rather than duplicating the files.
package skills

import "embed"

// FS holds every shipped skill, rooted at plugin/skills/ — one
// directory per skill, each containing SKILL.md (with a `version:`
// frontmatter line — the overwrite-refusal check in internal/init
// compares it) plus optional references/*.md.
//
// New skills must be added to this directive explicitly; an embed of
// `*` would drag this .go file along with it.
//
//go:embed pincher-loop pincher-onboard pincher-review pincher-debug pincher-steward
var FS embed.FS

// SPDX-License-Identifier: MIT

package init

import "strings"

// #243 — detect a hand-rolled pincher policy section in an existing
// markdown/text file so `pincher init` can canonicalize it instead of
// appending a duplicate block. The detector is conservative: it only
// fires on heading-bounded blocks (a markdown heading containing the
// word "pincher", case-insensitive). Loose `mcp__pincher__` mentions
// in code examples or top-level prose are left alone — wrapping them
// would risk accidentally clobbering content the user meant to keep.

// HasMarkers reports whether content already contains a paired set
// of pincher init markers. When true, the detector should NOT fire —
// markers take precedence and the replace path handles them.
func HasMarkers(content string) bool {
	startIdx := strings.Index(content, MarkerStart)
	endIdx := strings.Index(content, MarkerEnd)
	return startIdx >= 0 && endIdx > startIdx
}

// DetectPincherPolicySection scans content for a markdown heading
// whose text contains "pincher" (case-insensitive). When found,
// returns the byte range [start, end) covering that heading's
// section — from the heading line through the line BEFORE the next
// markdown heading at the same level OR EOF.
//
// Returns ok=false when no qualifying heading is found.
func DetectPincherPolicySection(content string) (start, end int, ok bool) {
	lines := strings.SplitAfter(content, "\n")
	pos := 0
	headingStart := -1
	headingLevel := 0
	for _, line := range lines {
		level, isHeading := MarkdownHeadingLevel(line)
		if isHeading {
			if headingStart >= 0 {
				if level <= headingLevel {
					return headingStart, pos, true
				}
			} else if level <= 6 && lineMentionsPincher(line) {
				headingStart = pos
				headingLevel = level
			}
		}
		pos += len(line)
	}
	if headingStart >= 0 {
		return headingStart, pos, true
	}
	return 0, 0, false
}

// MarkdownHeadingLevel returns (level, true) when line is a markdown
// ATX heading (`#`, `##`, etc., up to 6).
func MarkdownHeadingLevel(line string) (int, bool) {
	trimmed := strings.TrimLeft(line, " ")
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, false
	}
	if level == len(trimmed) || trimmed[level] != ' ' {
		return 0, false
	}
	return level, true
}

func lineMentionsPincher(line string) bool {
	return strings.Contains(strings.ToLower(line), "pincher")
}

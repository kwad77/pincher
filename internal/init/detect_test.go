// SPDX-License-Identifier: MIT

package init

import (
	"strings"
	"testing"
)

// Tests for #243 detect-and-wrap. The behavior matrix:
//
// | Existing file state                                   | Action          |
// | ----------------------------------------------------- | --------------- |
// | No file                                               | wrote (fresh)   |
// | File present, has markers                             | updated         |
// | File present, no markers, no detected pincher content | appended        |
// | File present, no markers, detected pincher content    | updated (NEW)   |

func TestDetectPincherPolicySection_HeadingBoundedBlock(t *testing.T) {
	content := `# CLAUDE.md

Some intro text.

## Pincher Usage Policy

Use pincher first. mcp__pincher__search etc.

More pincher content.

## Other Section

Unrelated content.
`
	start, end, ok := DetectPincherPolicySection(content)
	if !ok {
		t.Fatal("detector did not find heading-bounded pincher section")
	}
	block := content[start:end]
	if !strings.HasPrefix(block, "## Pincher Usage Policy") {
		t.Errorf("block must start with the heading; got prefix %q", block[:min(40, len(block))])
	}
	if strings.Contains(block, "## Other Section") {
		t.Error("block must NOT include the next-section heading")
	}
	if !strings.Contains(block, "More pincher content.") {
		t.Error("block must include all content under the heading until the next same-level one")
	}
}

func TestDetectPincherPolicySection_NoPincherMention(t *testing.T) {
	content := `# CLAUDE.md

Some content.

## Other Section

Nothing pincher-related.
`
	_, _, ok := DetectPincherPolicySection(content)
	if ok {
		t.Error("detector fired on content with no pincher heading")
	}
}

func TestDetectPincherPolicySection_LooseMentionWithoutHeading(t *testing.T) {
	// `mcp__pincher__` mentioned but in a code example, not as a heading.
	// The conservative detector should NOT fire — wrapping this would
	// risk clobbering content the user meant to keep.
	content := `# CLAUDE.md

Some intro.

## Examples

You can call mcp__pincher__search to find symbols.
`
	_, _, ok := DetectPincherPolicySection(content)
	if ok {
		t.Error("detector fired on a non-heading mention; conservative path should leave this alone")
	}
}

func TestDetectPincherPolicySection_HeadingExtendsToEOF(t *testing.T) {
	content := `# CLAUDE.md

## Pincher Usage Policy

Section content with no following heading.
`
	start, end, ok := DetectPincherPolicySection(content)
	if !ok {
		t.Fatal("detector did not find EOF-bounded pincher section")
	}
	block := content[start:end]
	if !strings.HasPrefix(block, "## Pincher Usage Policy") {
		t.Errorf("block must start with heading; got %q", block[:min(30, len(block))])
	}
	if !strings.Contains(block, "Section content with no following heading.") {
		t.Error("block must include trailing content up to EOF")
	}
}

func TestDetectPincherPolicySection_NestedSubheadingsStayInside(t *testing.T) {
	// A deeper sub-heading (### inside ##) should NOT close the section.
	content := `# CLAUDE.md

## Pincher Usage Policy

Intro.

### Subsection

Inside the pincher section.

## Next Same-Level Heading

Outside.
`
	start, end, ok := DetectPincherPolicySection(content)
	if !ok {
		t.Fatal("detector did not find pincher section")
	}
	block := content[start:end]
	if !strings.Contains(block, "### Subsection") || !strings.Contains(block, "Inside the pincher section.") {
		t.Error("block must include nested deeper subheadings as part of the same section")
	}
	if strings.Contains(block, "## Next Same-Level Heading") {
		t.Error("block must NOT include the next same-level heading")
	}
}

func TestMarkdownHeadingLevel_Cases(t *testing.T) {
	cases := []struct {
		line  string
		level int
		ok    bool
	}{
		{"# H1\n", 1, true},
		{"## H2\n", 2, true},
		{"###### H6\n", 6, true},
		{"####### Not a heading (>6)\n", 0, false},
		{"#hashtag\n", 0, false}, // no space
		{"  ## indented heading\n", 2, true},
		{"plain text\n", 0, false},
		{"\n", 0, false},
	}
	for _, tc := range cases {
		gotLevel, gotOK := MarkdownHeadingLevel(tc.line)
		if gotLevel != tc.level || gotOK != tc.ok {
			t.Errorf("MarkdownHeadingLevel(%q) = (%d, %v), want (%d, %v)",
				tc.line, gotLevel, gotOK, tc.level, tc.ok)
		}
	}
}

// Behavior matrix integration: hand-rolled section without markers.
// Pre-fix this would APPEND a duplicate; post-fix it should REPLACE
// (action="updated") with the canonical embedded policy.
func TestMergePolicyBlockBare_DetectAndReplaceUnmarkered(t *testing.T) {
	existing := `# CLAUDE.md

## Pincher Usage Policy

Hand-rolled policy text the user wrote.
`
	merged, action := MergePolicyBlockBare(existing, "CANONICAL POLICY BODY")
	if action != "updated" {
		t.Errorf("action = %q, want \"updated\" (detector should treat unmarkered pincher heading as the managed block)", action)
	}
	if strings.Contains(merged, "Hand-rolled policy text the user wrote.") {
		t.Error("merged content still has user's hand-rolled text; the detector should have triggered a replace")
	}
	if !strings.Contains(merged, "CANONICAL POLICY BODY") {
		t.Error("merged content missing the canonical policy body")
	}
	// Markers must be present after the merge — subsequent runs should
	// take the marker path, not the detector path.
	if !strings.Contains(merged, MarkerStart) || !strings.Contains(merged, MarkerEnd) {
		t.Error("merged content missing pincher init markers; subsequent runs would re-detect from scratch")
	}
}

// Pre-existing markers always win over detection — the marker path is
// the canonical update path.
func TestMergePolicyBlockBare_MarkersPrecedeDetection(t *testing.T) {
	existing := `# CLAUDE.md

## Pincher Usage Policy

Hand-rolled text outside markers.

` + MarkerStart + `
old marker block
` + MarkerEnd + `
`
	merged, action := MergePolicyBlockBare(existing, "NEW POLICY BODY")
	if action != "updated" {
		t.Errorf("action = %q, want \"updated\" (marker path should fire)", action)
	}
	// The user's hand-rolled text outside the markers must survive —
	// markers take precedence; we don't fold the heading-bounded section
	// into the marker block when markers already exist.
	if !strings.Contains(merged, "Hand-rolled text outside markers.") {
		t.Error("hand-rolled text outside markers was clobbered by the detector — marker path should not invoke detection")
	}
	if !strings.Contains(merged, "NEW POLICY BODY") {
		t.Error("merged content missing the new policy body")
	}
	if strings.Contains(merged, "old marker block") {
		t.Error("old marker block was not replaced")
	}
}

// File without any pincher mention: append at end (unchanged behavior).
func TestMergePolicyBlockBare_NoPincherContentAppends(t *testing.T) {
	existing := `# Some Other File

Random content.

## Some Section

More content.
`
	merged, action := MergePolicyBlockBare(existing, "POLICY")
	if action != "appended" {
		t.Errorf("action = %q, want \"appended\" (no pincher content present, no markers, conservative path)", action)
	}
	if !strings.Contains(merged, "Random content.") || !strings.Contains(merged, "## Some Section") {
		t.Error("merged content lost the original file body")
	}
}

// Empty existing content: fresh write, action="wrote" (unchanged).
func TestMergePolicyBlockBare_EmptyExistingWrites(t *testing.T) {
	merged, action := MergePolicyBlockBare("", "POLICY BODY")
	if action != "wrote" {
		t.Errorf("action = %q, want \"wrote\"", action)
	}
	if !strings.Contains(merged, "POLICY BODY") {
		t.Error("merged content missing the policy body")
	}
}

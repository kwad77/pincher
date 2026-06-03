// SPDX-License-Identifier: MIT

package init

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPincherPolicyMarkdown_NotEmptyAndContainsHeading(t *testing.T) {
	if strings.TrimSpace(PolicyMarkdown) == "" {
		t.Fatal("embedded pincher policy must not be empty")
	}
	if !strings.Contains(PolicyMarkdown, "Pincher Usage Policy") {
		t.Fatal("embedded policy missing the 'Pincher Usage Policy' heading")
	}
}

func TestMergePolicyBlock_FromEmpty(t *testing.T) {
	out, action := MergePolicyBlock("", "## Test policy\n")
	if action != "wrote" {
		t.Errorf("action=%q, want 'wrote'", action)
	}
	if !strings.Contains(out, "# CLAUDE.md") {
		t.Error("expected new file to include the standard CLAUDE.md header")
	}
	if !strings.Contains(out, MarkerStart) || !strings.Contains(out, MarkerEnd) {
		t.Error("expected both markers in new file")
	}
	if !strings.Contains(out, "## Test policy") {
		t.Error("expected policy body in new file")
	}
}

func TestMergePolicyBlock_AppendToExisting(t *testing.T) {
	existing := "# Project rules\n\nFollow our internal docs.\n"
	out, action := MergePolicyBlock(existing, "## Pincher\n")
	if action != "appended" {
		t.Errorf("action=%q, want 'appended'", action)
	}
	if !strings.Contains(out, "Project rules") {
		t.Error("existing content should be preserved")
	}
	if !strings.Contains(out, MarkerStart) {
		t.Error("expected start marker")
	}
	startIdx := strings.Index(out, MarkerStart)
	rulesIdx := strings.Index(out, "Project rules")
	if rulesIdx >= startIdx {
		t.Error("existing content should appear before the pincher block")
	}
}

// #778: init used to inject the LF-built policy block into a CRLF file,
// leaving mixed line endings (and a dangling lone \r on the append
// path). Every CRLF-input path must produce uniform-CRLF output; LF
// input must stay LF.
func TestMergePolicyBlock_PreservesLineEndings(t *testing.T) {
	noMixed := func(t *testing.T, label, out string) {
		t.Helper()
		// A uniform-CRLF string has no bare \n (one not preceded by \r)
		// and no bare \r (one not followed by \n).
		lf := strings.ReplaceAll(out, "\r\n", "")
		if strings.ContainsAny(lf, "\r\n") {
			t.Errorf("%s: output has mixed/bare line endings — every \\n must be part of a \\r\\n pair", label)
		}
	}

	t.Run("CRLF append path", func(t *testing.T) {
		existing := "# Project rules\r\n\r\nFollow our internal docs.\r\n"
		out, action := MergePolicyBlock(existing, "## Pincher\n")
		if action != "appended" {
			t.Fatalf("action=%q, want appended", action)
		}
		noMixed(t, "append", out)
	})

	t.Run("CRLF replace path", func(t *testing.T) {
		existing := "# CLAUDE.md\r\n\r\nIntro.\r\n\r\n" +
			MarkerStart + "\r\n" + "OLD\r\n" + MarkerEnd + "\r\n\r\nTrailing.\r\n"
		out, action := MergePolicyBlock(existing, "## NEW\n")
		if action != "updated" {
			t.Fatalf("action=%q, want updated", action)
		}
		noMixed(t, "replace", out)
		if strings.Contains(out, "OLD") {
			t.Error("old block content should be gone")
		}
		if !strings.Contains(out, "Trailing.") {
			t.Error("trailing content should survive")
		}
	})

	t.Run("LF input stays LF", func(t *testing.T) {
		existing := "# Project rules\n\nDocs.\n"
		out, _ := MergePolicyBlock(existing, "## Pincher\n")
		if strings.Contains(out, "\r") {
			t.Error("LF-origin file must not gain CRLF endings")
		}
	})

	// #779: the embedded policy.md is checked out CRLF on Windows, so the
	// policy arg itself can carry \r\n. A naive \n→\r\n restore turned
	// those into \r\r\n and the bad-\r count drifted between runs,
	// breaking idempotency. Drive the regression with a CRLF policy.
	t.Run("CRLF policy input does not double-convert", func(t *testing.T) {
		crlfPolicy := "## Pincher\r\nuse pincher.\r\n"

		t.Run("CRLF file stays uniform CRLF", func(t *testing.T) {
			existing := "# Project rules\r\n\r\nDocs.\r\n"
			out, _ := MergePolicyBlock(existing, crlfPolicy)
			noMixed(t, "crlf-policy", out)
			if strings.Contains(out, "\r\r") {
				t.Error("output has \\r\\r — policy block was double-converted")
			}
		})

		t.Run("idempotent across reruns", func(t *testing.T) {
			first, _ := MergePolicyBlock("", crlfPolicy)
			second, _ := MergePolicyBlock(first, crlfPolicy)
			if first != second {
				t.Errorf("re-run not idempotent with CRLF policy\n--- first ---\n%q\n--- second ---\n%q", first, second)
			}
		})
	})
}

func TestMergePolicyBlock_ReplaceExistingBlock(t *testing.T) {
	existing := "# CLAUDE.md\n\nIntro.\n\n" +
		MarkerStart + "\n" +
		"OLD CONTENT THAT SHOULD GET REPLACED\n" +
		MarkerEnd + "\n\n" +
		"Trailing text.\n"
	out, action := MergePolicyBlock(existing, "## NEW POLICY\n")
	if action != "updated" {
		t.Errorf("action=%q, want 'updated'", action)
	}
	if strings.Contains(out, "OLD CONTENT") {
		t.Error("old content should be removed")
	}
	if !strings.Contains(out, "## NEW POLICY") {
		t.Error("new content should be present")
	}
	if !strings.Contains(out, "Intro.") {
		t.Error("content before block should survive")
	}
	if !strings.Contains(out, "Trailing text.") {
		t.Error("content after block should survive")
	}
	if strings.Count(out, MarkerStart) != 1 || strings.Count(out, MarkerEnd) != 1 {
		t.Errorf("expected exactly one marker pair, got start=%d end=%d",
			strings.Count(out, MarkerStart), strings.Count(out, MarkerEnd))
	}
}

func TestMergePolicyBlock_Idempotent(t *testing.T) {
	policy := "## Pincher\nuse pincher.\n"
	first, _ := MergePolicyBlock("", policy)
	second, action := MergePolicyBlock(first, policy)
	if action != "updated" {
		t.Errorf("re-run action=%q, want 'updated'", action)
	}
	if first != second {
		t.Errorf("re-running with same input should produce identical output\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestMergePolicyBlock_OnlyStartMarker_AppendsNewBlock(t *testing.T) {
	existing := "# Project\n\n" + MarkerStart + "\nbroken\n"
	out, action := MergePolicyBlock(existing, "## Body\n")
	if action != "appended" {
		t.Errorf("action=%q, want 'appended' (malformed → append safely)", action)
	}
	if !strings.Contains(out, "broken") {
		t.Error("the original malformed text should be preserved (we don't auto-recover)")
	}
}

func TestResolveCLAUDEPath_ProjectDefault(t *testing.T) {
	tmp := t.TempDir()
	got, err := ResolveCLAUDEPath(tmp, false)
	if err != nil {
		t.Fatalf("ResolveCLAUDEPath: %v", err)
	}
	gotResolved, _ := filepath.EvalSymlinks(filepath.Dir(got))
	wantResolved, _ := filepath.EvalSymlinks(tmp)
	if gotResolved != wantResolved {
		t.Errorf("got dir %q, want dir %q", gotResolved, wantResolved)
	}
	if filepath.Base(got) != "CLAUDE.md" {
		t.Errorf("got base %q, want CLAUDE.md", filepath.Base(got))
	}
}

func TestResolveCLAUDEPath_Global(t *testing.T) {
	got, err := ResolveCLAUDEPath(t.TempDir(), true)
	if err != nil {
		t.Fatalf("ResolveCLAUDEPath(true): %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".claude", "CLAUDE.md")) {
		t.Errorf("global path %q should end with .claude/CLAUDE.md", got)
	}
}

// WriteFileEnsuringDir creates intermediate directories for new file
// paths. Pin the behavior because the MCP handler relies on it for
// targets like cursor whose path includes `.cursor/rules/` that may
// not exist yet.
func TestWriteFileEnsuringDir_CreatesIntermediates(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "nested", "deeper", "file.txt")
	if err := WriteFileEnsuringDir(target, "hello"); err != nil {
		t.Fatalf("WriteFileEnsuringDir: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("file content = %q, want %q", string(got), "hello")
	}
}

// HasMarkers must distinguish "matched pair" from "only one marker"
// so the unmarkered detector path doesn't fire when a half-broken
// marker pair is present (the malformed-fallback case).
func TestHasMarkers_RequiresPair(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"both markers", MarkerStart + "\nx\n" + MarkerEnd, true},
		{"only start", MarkerStart + "\nx", false},
		{"only end", "x\n" + MarkerEnd, false},
		{"neither", "no markers here", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasMarkers(c.body); got != c.want {
				t.Errorf("HasMarkers(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

// #803/#849: a dry-run reports "would <verb>" — Action is past tense
// ("wrote"/"updated"/"appended"), so it must be mapped to the base verb
// or it reads ungrammatically ("would wrote", "would_updated"). Shared
// by the CLI text path and the MCP JSON `action` field.
func TestPresentTenseAction(t *testing.T) {
	cases := map[string]string{
		"wrote":     "write",
		"updated":   "update",
		"appended":  "append",
		"unchanged": "unchanged", // pass-through
		"error":     "error",     // pass-through
		"":          "",          // pass-through
	}
	for in, want := range cases {
		if got := PresentTenseAction(in); got != want {
			t.Errorf("PresentTenseAction(%q) = %q, want %q", in, got, want)
		}
	}
}

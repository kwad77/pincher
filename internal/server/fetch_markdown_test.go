// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #579: extractTextFromHTML ran on every fetched URL regardless of
// Content-Type and consumed `>` unconditionally even outside tags.
// Markdown documents with arrows (`=>`), generics (`Vec<T>`), or
// blockquotes (`> note`) silently lost characters; the title fell
// back to the URL because there's no `<title>` tag in markdown.

func TestHandleFetch_MarkdownContent_PreservesArrowsAndGenericSyntax(t *testing.T) {
	t.Parallel()
	srv, _ := fetchTestSetup(t)
	srv.fetchAllowLoopback = true

	body := `# Project Title

Here's an arrow function: ` + "`const f = () => x + 1`" + ` and a generic ` + "`Vec<T>`" + `.

> Important: the literal ` + "`>`" + ` belongs in the body.

Pincher's HTTP path: ` + "`/v1/<tool>`" + `.
`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(body))
	}))
	defer upstream.Close()

	result, err := srv.handleFetch(context.Background(), makeReq(fetchArgs(upstream.URL)))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	m := decode(t, result)

	// H1 → title (NOT the URL fallback).
	if m["title"] != "Project Title" {
		t.Errorf("title = %v; want %q (H1 of markdown body)", m["title"], "Project Title")
	}

	text, _ := m["text"].(string)
	for _, want := range []string{"=>", "<T>", "<tool>", "> Important"} {
		if !strings.Contains(text, want) {
			t.Errorf("fetched markdown text missing %q — markdown corruption regression (#579)\nfull text:\n%s", want, text)
		}
	}
}

func TestExtractTextFromHTML_PreservesLiteralAngleBracketsOutsideTags(t *testing.T) {
	t.Parallel()
	// Direct unit test of the scanner — pre-fix it consumed `>`
	// even outside any tag.
	in := `<p>Use Vec&lt;T&gt; or write x > 0 inline.</p>`
	_, text := extractTextFromHTML(in)
	if !strings.Contains(text, "x > 0") {
		t.Errorf("scanner ate the `>` outside the tag context: %q", text)
	}
}

func TestFirstMarkdownH1_SkipsFrontMatter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "# Hello\n\nbody", "Hello"},
		{"yaml-front-matter", "---\ntitle: x\n---\n# Real\n\nbody", "Real"},
		{"toml-front-matter", "+++\ntitle = \"x\"\n+++\n# Real\n\nbody", "Real"},
		{"no-h1", "Just text", ""},
		{"h1-after-blanks", "\n\n\n# Late\n", "Late"},
	}
	for _, c := range cases {
		got := firstMarkdownH1(c.in)
		if got != c.want {
			t.Errorf("firstMarkdownH1(%q) = %q; want %q", c.name, got, c.want)
		}
	}
}

// #1427: bash comments like `# 1. Install` inside ```bash fenced
// blocks were being matched as Markdown H1s when the real document
// had none — pincher's own README has zero H1s but `fetch` titled
// the stored Document "1. Install" because the Quick Start code
// block's `# 1. Install` comment was picked up. Track fence state
// so anything inside ```...``` (or ~~~...~~~) is skipped.
func TestFirstMarkdownH1_SkipsFencedCodeBlocks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "h1-inside-bash-fence-ignored",
			in:   "Body text.\n\n```bash\n# 1. Install\ngo install x\n```\n\n# Real Title",
			want: "Real Title",
		},
		{
			name: "no-real-h1-only-fenced-comment",
			in:   "Body text.\n\n```bash\n# 1. Install\ngo install x\n```\n\nmore body",
			want: "",
		},
		{
			name: "no-real-h1-only-tilde-fence-comment",
			in:   "Body.\n\n~~~bash\n# Hidden\n~~~\n\n",
			want: "",
		},
		{
			name: "h1-before-fence-still-wins",
			in:   "# Top Title\n\n```bash\n# decoy\n```\n",
			want: "Top Title",
		},
		{
			name: "multiple-fences-state-tracks-correctly",
			in:   "```\n# decoy1\n```\nbody\n```\n# decoy2\n```\n# Real",
			want: "Real",
		},
	}
	for _, c := range cases {
		got := firstMarkdownH1(c.in)
		if got != c.want {
			t.Errorf("firstMarkdownH1(%q): got %q, want %q (#1427)", c.name, got, c.want)
		}
	}
}

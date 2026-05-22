package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

func TestShortNameFromID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"internal/db/db.go::db.Open#Function", "Open"},
		{"a.go::pkg.Type.Method#Method", "Method"},
		{"a.go::bare#Function", "bare"},
		{"weird-id-no-separators", "weird-id-no-separators"},
	}
	for _, c := range cases {
		if got := shortNameFromID(c.in); got != c.want {
			t.Errorf("shortNameFromID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMermaidLabel(t *testing.T) {
	if got := mermaidLabel(`say "hi"`); got != `say #quot;hi#quot;` {
		t.Errorf("mermaidLabel quote escape = %q", got)
	}
	if got := mermaidLabel("two\nlines"); got != "two lines" {
		t.Errorf("mermaidLabel newline = %q", got)
	}
}

// callflowTestStore indexes a three-link Go call chain and returns the
// store + project id. funcA → funcB → funcC.
func callflowTestStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	dir := t.TempDir()
	src := `package chain

func funcC() int { return 3 }

func funcB() int { return funcC() }

func funcA() int { return funcB() }
`
	if err := os.WriteFile(filepath.Join(dir, "chain.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	res, err := index.New(store).Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	return store, res.ProjectID
}

func TestCollectCallflow_BothDirections(t *testing.T) {
	store, projectID := callflowTestStore(t)
	seedID, err := resolveCallflowSeed(store, projectID, "funcB")
	if err != nil {
		t.Fatalf("resolveCallflowSeed: %v", err)
	}

	nodes, edges, truncated := collectCallflow(store, seedID, "both", 2)
	if truncated {
		t.Error("3-node chain should not truncate")
	}
	// funcB reaches funcA (caller) and funcC (callee).
	for _, want := range []string{"funcA", "funcB", "funcC"} {
		found := false
		for id := range nodes {
			if strings.Contains(id, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("node set missing %s; got %v", want, nodes)
		}
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 edges (A->B, B->C); got %d: %v", len(edges), edges)
	}
}

func TestCollectCallflow_CalleesOnly(t *testing.T) {
	store, projectID := callflowTestStore(t)
	seedID, _ := resolveCallflowSeed(store, projectID, "funcB")

	nodes, _, _ := collectCallflow(store, seedID, "callees", 2)
	// callees-only from funcB: funcB, funcC — never funcA.
	for id := range nodes {
		if strings.Contains(id, "funcA") {
			t.Errorf("callees-only walk must not reach the caller funcA; got %v", nodes)
		}
	}
}

func TestRenderCallflowMermaid(t *testing.T) {
	store, projectID := callflowTestStore(t)
	seedID, _ := resolveCallflowSeed(store, projectID, "funcB")
	nodes, edges, _ := collectCallflow(store, seedID, "both", 2)

	out, err := renderCallflowMermaid(store, projectID, seedID, nodes, edges, false)
	if err != nil {
		t.Fatalf("renderCallflowMermaid: %v", err)
	}
	if !strings.Contains(out, "flowchart LR") {
		t.Errorf("missing flowchart header:\n%s", out)
	}
	if !strings.Contains(out, ":::seed") {
		t.Errorf("seed node not highlighted:\n%s", out)
	}
	if !strings.Contains(out, "-->") {
		t.Errorf("no edges rendered:\n%s", out)
	}
	if !strings.Contains(out, "classDef seed") {
		t.Errorf("missing seed classDef:\n%s", out)
	}
}

func TestResolveCallflowSeed_UnknownName(t *testing.T) {
	store, projectID := callflowTestStore(t)
	if _, err := resolveCallflowSeed(store, projectID, "noSuchSymbol"); err == nil {
		t.Error("expected error for an unknown symbol name")
	}
}

// TestCallflowCLI_EndToEnd indexes a project into a temp data dir and
// drives callflowCLI through the real flag/open/resolve/render path.
func TestCallflowCLI_EndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	projDir := t.TempDir()
	src := "package chain\n\nfunc funcC() int { return 3 }\n\n" +
		"func funcB() int { return funcC() }\n\nfunc funcA() int { return funcB() }\n"
	if err := os.WriteFile(filepath.Join(projDir, "chain.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	store, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	res, err := index.New(store).Index(context.Background(), projDir, false)
	if err != nil {
		store.Close()
		t.Fatalf("index: %v", err)
	}
	project, _ := store.GetProject(res.ProjectID)
	store.Close()

	t.Run("renders to stdout", func(t *testing.T) {
		var out, errb strings.Builder
		code := callflowCLI([]string{"--data-dir", dataDir, "--project", project.Name,
			"--symbol", "funcB"}, &out, &errb)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
		}
		if !strings.Contains(out.String(), "flowchart LR") {
			t.Errorf("stdout is not a Mermaid flowchart:\n%s", out.String())
		}
	})

	t.Run("missing --symbol exits 1", func(t *testing.T) {
		var out, errb strings.Builder
		if code := callflowCLI([]string{"--data-dir", dataDir}, &out, &errb); code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})

	t.Run("bad direction exits 1", func(t *testing.T) {
		var out, errb strings.Builder
		code := callflowCLI([]string{"--data-dir", dataDir, "--project", project.Name,
			"--symbol", "funcB", "--direction", "sideways"}, &out, &errb)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})

	t.Run("unknown symbol exits 1", func(t *testing.T) {
		var out, errb strings.Builder
		code := callflowCLI([]string{"--data-dir", dataDir, "--project", project.Name,
			"--symbol", "noSuchThing"}, &out, &errb)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})
}

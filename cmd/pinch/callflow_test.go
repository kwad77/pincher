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

// TestCollectCallflow_TruncatesAtNodeCap builds a fixture wider than
// callflowNodeCap and checks the BFS reports truncation and the
// rendered diagram carries the truncation note.
func TestCollectCallflow_TruncatesAtNodeCap(t *testing.T) {
	dir := t.TempDir()
	src := "package wide\n\nfunc hubFn() int { return 0 }\n"
	for i := 0; i < callflowNodeCap+20; i++ {
		src += "func use" + itoaCF(i) + "() { hubFn() }\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "wide.go"), []byte(src), 0o644); err != nil {
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
	seedID, err := resolveCallflowSeed(store, res.ProjectID, "hubFn")
	if err != nil {
		t.Fatalf("resolveCallflowSeed: %v", err)
	}

	nodes, edges, truncated := collectCallflow(store, seedID, "callers", 2)
	if !truncated {
		t.Errorf("a %d-caller fixture should truncate at the %d-node cap", callflowNodeCap+20, callflowNodeCap)
	}
	if len(nodes) > callflowNodeCap {
		t.Errorf("node set %d exceeds cap %d", len(nodes), callflowNodeCap)
	}
	out, err := renderCallflowMermaid(store, res.ProjectID, seedID, nodes, edges, truncated)
	if err != nil {
		t.Fatalf("renderCallflowMermaid: %v", err)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("truncated diagram should carry a truncation note:\n%s", out[:min(len(out), 200)])
	}
}

// itoaCF is a tiny int→string for fixture generation (avoids importing
// strconv just for the test).
func itoaCF(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
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

// resolveCallflowSeed accepts a full symbol id (the `::`-bearing path)
// as well as a short name.
func TestResolveCallflowSeed_ByFullID(t *testing.T) {
	store, projectID := callflowTestStore(t)
	// First resolve by name to learn the concrete id, then feed that id
	// back through the id branch.
	id, err := resolveCallflowSeed(store, projectID, "funcB")
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if !strings.Contains(id, "::") {
		t.Fatalf("expected a full id with ::, got %q", id)
	}
	got, err := resolveCallflowSeed(store, projectID, id)
	if err != nil {
		t.Fatalf("resolve by id: %v", err)
	}
	if got != id {
		t.Errorf("resolve by id = %q, want %q", got, id)
	}
	// An unknown id is rejected.
	if _, err := resolveCallflowSeed(store, projectID, "no/such.go::x.Y#Function"); err == nil {
		t.Error("expected error for an unknown symbol id")
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

	t.Run("out to file with clamped depth", func(t *testing.T) {
		var out, errb strings.Builder
		dest := filepath.Join(t.TempDir(), "cf.mmd")
		// --depth=99 exercises the upper clamp; --out exercises the file
		// path + the receipt line.
		code := callflowCLI([]string{"--data-dir", dataDir, "--project", project.Name,
			"--symbol", "funcB", "--depth", "99", "--out", dest}, &out, &errb)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
		}
		blob, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read export: %v", err)
		}
		if !strings.Contains(string(blob), "flowchart LR") {
			t.Errorf("export file is not a Mermaid flowchart:\n%s", blob)
		}
		if !strings.Contains(errb.String(), "wrote call-flow") {
			t.Errorf("stderr should print a receipt; got %s", errb.String())
		}
	})
}

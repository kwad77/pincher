// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #2014: the advise-index advisory. Production telemetry found 100%
// hook pass-through because 725/726 events targeted files outside
// every indexed root — the redirect mechanism can't act where there's
// no index. When a session repeatedly Reads code files inside an
// UNINDEXED git repo, the hook now emits a one-time "index this repo"
// advisory (decision value: advise_index). Guardrails under test:
// threshold N>=3 per (session, root), once per root per session,
// never for indexed repos, never for non-repo dirs, never for $HOME,
// always advisory (Continue=true on every branch).

// newUnindexedGitRepo creates a temp dir with a .git marker and the
// given code files. Nothing is indexed — the repo is exactly the
// production case the telemetry audit surfaced.
func newUnindexedGitRepo(t *testing.T, files ...string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	for _, f := range files {
		abs := filepath.Join(repo, f)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte("package main\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return repo
}

// fireReadHook runs one full hook cycle the way runHookCheckCLI does:
// decide, then log. Logging matters here — the #2014 threshold counter
// IS the hook_invocations rows prior invocations wrote.
func fireReadHook(t *testing.T, store *db.Store, sessionID, path string) hookDecision {
	t.Helper()
	in := hookCheckInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": path},
		SessionID: sessionID,
	}
	d := decideHook(store, in, false)
	logHookDecision(store, in, d)
	return d
}

func TestDecideHook_Read_UnindexedGitRepo_ThirdEventAdvises_FourthSilent(t *testing.T) {
	store := newHookTestStore(t)
	repo := newUnindexedGitRepo(t, "main.go", "internal/db.go", "internal/api.go")

	// Events 1 and 2: below threshold, silent pass-through.
	for i, f := range []string{"main.go", "internal/db.go"} {
		d := fireReadHook(t, store, "sess-2014", filepath.Join(repo, f))
		if !d.Continue || d.Decision != "pass_through" || d.SystemMessage != "" {
			t.Fatalf("event %d: want silent pass_through below threshold, got %+v", i+1, d)
		}
	}

	// Event 3: threshold met — one-time advisory, still non-blocking.
	d := fireReadHook(t, store, "sess-2014", filepath.Join(repo, "internal/api.go"))
	if !d.Continue {
		t.Fatalf("advisory must never block (Continue=false): %+v", d)
	}
	if d.Decision != "advise_index" {
		t.Fatalf("third event decision = %q, want advise_index", d.Decision)
	}
	if !strings.Contains(d.SystemMessage, "pincher index "+repo) {
		t.Errorf("advisory should name the exact command; got %q", d.SystemMessage)
	}
	if d.SuggestedTool != "index" {
		t.Errorf("suggested tool = %q, want index", d.SuggestedTool)
	}
	if !strings.Contains(d.SuggestedArgs, repo) {
		t.Errorf("suggested args should carry the repo root; got %s", d.SuggestedArgs)
	}
	// Telemetry contract: the advisory row's file_path is the repo
	// root — the suppression key and the take-rate join key.
	if d.FilePathParsed != repo {
		t.Errorf("advisory file_path = %q, want repo root %q", d.FilePathParsed, repo)
	}

	// Event 4 (and beyond): once per root per session — silent forever.
	for i := 0; i < 3; i++ {
		d := fireReadHook(t, store, "sess-2014", filepath.Join(repo, "main.go"))
		if d.Decision != "pass_through" || d.SystemMessage != "" {
			t.Fatalf("post-advisory event %d: want silent pass_through, got %+v", i+4, d)
		}
	}
}

func TestDecideHook_Read_IndexedRepo_NeverAdvises(t *testing.T) {
	store := newHookTestStore(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	// Index the repo root as a project (with one indexed file so the
	// project is real).
	indexLargeFakeFile(t, store, repo, "internal/server/server.go", 50000)

	// Read a file the indexer has NOT seen (no file_hashes row) inside
	// the indexed repo — the unindexed-file branch, but the root is
	// covered, so no recruitment advisory, ever.
	fresh := filepath.Join(repo, "cmd", "new.go")
	if err := os.MkdirAll(filepath.Dir(fresh), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(fresh, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for i := 0; i < 5; i++ {
		d := fireReadHook(t, store, "sess-indexed", fresh)
		if d.Decision == "advise_index" {
			t.Fatalf("event %d: advise_index inside an indexed repo (guardrail breach): %+v", i+1, d)
		}
	}
}

func TestDecideHook_Read_NonGitDir_NeverAdvises(t *testing.T) {
	store := newHookTestStore(t)
	dir := t.TempDir() // no .git anywhere up to the temp root
	f := filepath.Join(dir, "script.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for i := 0; i < 5; i++ {
		d := fireReadHook(t, store, "sess-nogit", f)
		if d.Decision != "pass_through" || d.SystemMessage != "" {
			t.Fatalf("event %d: non-repo dir must stay silent pass_through, got %+v", i+1, d)
		}
	}
}

func TestDecideHook_Read_HomeAsRepoRoot_NeverAdvises(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME faking is POSIX-shaped; the guard itself is covered by index.IsBloatTrap tests")
	}
	store := newHookTestStore(t)
	// Fake $HOME as a git repo — the #1991 nightmare shape. The bloat
	// trap must refuse it no matter how much traffic it gets.
	home := newUnindexedGitRepo(t, "dotfiles.go")
	t.Setenv("HOME", home)
	for i := 0; i < 5; i++ {
		d := fireReadHook(t, store, "sess-home", filepath.Join(home, "dotfiles.go"))
		if d.Decision == "advise_index" {
			t.Fatalf("event %d: advised indexing $HOME (guardrail breach): %+v", i+1, d)
		}
	}
}

func TestDecideHook_Read_ThresholdIsPerSession(t *testing.T) {
	store := newHookTestStore(t)
	repo := newUnindexedGitRepo(t, "a.go", "b.go")

	// Two events in session A, two in session B: neither crosses N=3.
	for _, sess := range []string{"sess-a", "sess-b"} {
		for _, f := range []string{"a.go", "b.go"} {
			d := fireReadHook(t, store, sess, filepath.Join(repo, f))
			if d.Decision != "pass_through" {
				t.Fatalf("session %s below threshold: got %+v", sess, d)
			}
		}
	}
	// Third event in A advises; B's counter is untouched by A's rows.
	if d := fireReadHook(t, store, "sess-a", filepath.Join(repo, "a.go")); d.Decision != "advise_index" {
		t.Fatalf("third event in sess-a should advise, got %+v", d)
	}
	// A's advisory does not suppress B: B's own third event advises.
	if d := fireReadHook(t, store, "sess-b", filepath.Join(repo, "a.go")); d.Decision != "advise_index" {
		t.Fatalf("third event in sess-b should advise independently, got %+v", d)
	}
	// A new session starts from zero (threshold reset per session).
	if d := fireReadHook(t, store, "sess-c", filepath.Join(repo, "a.go")); d.Decision != "pass_through" {
		t.Fatalf("fresh session must start below threshold, got %+v", d)
	}
}

func TestDecideHook_Read_AdviseIndexGuards(t *testing.T) {
	t.Run("no session id never advises", func(t *testing.T) {
		store := newHookTestStore(t)
		repo := newUnindexedGitRepo(t, "a.go")
		for i := 0; i < 5; i++ {
			d := fireReadHook(t, store, "", filepath.Join(repo, "a.go"))
			if d.Decision != "pass_through" {
				t.Fatalf("event %d without session id: got %+v", i+1, d)
			}
		}
	})
	t.Run("non-code files never trigger", func(t *testing.T) {
		store := newHookTestStore(t)
		repo := newUnindexedGitRepo(t)
		f := filepath.Join(repo, "config.yaml")
		if err := os.WriteFile(f, []byte("k: v\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		for i := 0; i < 5; i++ {
			d := fireReadHook(t, store, "sess-yaml", f)
			if d.Decision != "pass_through" || d.SystemMessage != "" {
				t.Fatalf("event %d on yaml: got %+v", i+1, d)
			}
		}
	})
	t.Run("counter counts code reads only", func(t *testing.T) {
		store := newHookTestStore(t)
		repo := newUnindexedGitRepo(t, "a.go")
		yaml := filepath.Join(repo, "c.yaml")
		if err := os.WriteFile(yaml, []byte("k: v\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		// Two yaml reads + one code read = one code event, not three.
		fireReadHook(t, store, "sess-mix", yaml)
		fireReadHook(t, store, "sess-mix", yaml)
		if d := fireReadHook(t, store, "sess-mix", filepath.Join(repo, "a.go")); d.Decision != "pass_through" {
			t.Fatalf("non-code rows must not count toward the threshold: %+v", d)
		}
	})
	t.Run("threshold is per root", func(t *testing.T) {
		store := newHookTestStore(t)
		repoA := newUnindexedGitRepo(t, "a.go")
		repoB := newUnindexedGitRepo(t, "b.go")
		// Two events in repo A + one in repo B: neither root is at 3.
		fireReadHook(t, store, "sess-roots", filepath.Join(repoA, "a.go"))
		fireReadHook(t, store, "sess-roots", filepath.Join(repoA, "a.go"))
		if d := fireReadHook(t, store, "sess-roots", filepath.Join(repoB, "b.go")); d.Decision != "advise_index" {
			// repo B has 1 event — must not inherit repo A's count.
			if d.Decision != "pass_through" {
				t.Fatalf("repo B first event: got %+v", d)
			}
		} else {
			t.Fatalf("repo B advised on its first event (count leaked across roots): %+v", d)
		}
		// Repo A's third event advises for root A specifically.
		d := fireReadHook(t, store, "sess-roots", filepath.Join(repoA, "a.go"))
		if d.Decision != "advise_index" || !strings.Contains(d.SuggestedArgs, repoA) {
			t.Fatalf("repo A third event should advise for root A: %+v", d)
		}
	})
}

// TestDecideHook_Read_AdviseIndexRowLandsInTelemetry pins the
// measurability contract: the advisory writes a hook_invocations row
// with decision='advise_index' and file_path=<repo root>, the exact
// shape the take-rate query joins against projects.path.
func TestDecideHook_Read_AdviseIndexRowLandsInTelemetry(t *testing.T) {
	store := newHookTestStore(t)
	repo := newUnindexedGitRepo(t, "a.go", "b.go", "c.go")
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		fireReadHook(t, store, "sess-telemetry", filepath.Join(repo, f))
	}
	if !store.HookIndexAdvisedForRoot("sess-telemetry", repo) {
		t.Fatal("advise_index row not found for (session, root) after threshold crossing")
	}
	if store.HookIndexAdvisedForRoot("sess-other", repo) {
		t.Error("advise_index row leaked across sessions")
	}
	if store.HookIndexAdvisedForRoot("sess-telemetry", filepath.Join(repo, "sub")) {
		t.Error("advise_index suppression key must be the exact root")
	}
}

// runHookCheckForTest drives runHookCheckCLI end-to-end via the same
// stdin/stdout swap the other shim tests use, returning the parsed
// JSON response line.
func runHookCheckForTest(t *testing.T, dataDir, input string) map[string]any {
	t.Helper()
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("stdin temp: %v", err)
	}
	in.WriteString(input)
	in.Close()
	stdinFile, err := os.Open(in.Name())
	if err != nil {
		t.Fatalf("stdin open: %v", err)
	}
	defer stdinFile.Close()

	outFile, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("stdout temp: %v", err)
	}
	defer outFile.Close()

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin = stdinFile
	os.Stdout = outFile
	defer func() { os.Stdin = origStdin; os.Stdout = origStdout }()

	runHookCheckCLI([]string{"--data-dir", dataDir})

	outFile.Sync()
	body, err := os.ReadFile(outFile.Name())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(body))), &resp); err != nil {
		t.Fatalf("response not JSON: %v (%q)", err, body)
	}
	return resp
}

// TestHookCheckCLI_AdviseIndex_EndToEnd drives the real stdin/stdout
// shim (not just decideHook) so the wire shape is pinned: third event
// emits continue:true plus a systemMessage naming `pincher index`.
func TestHookCheckCLI_AdviseIndex_EndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	repo := newUnindexedGitRepo(t, "x.go")

	run := func(i int) map[string]any {
		t.Helper()
		input := fmt.Sprintf(
			`{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":%q},"session_id":"sess-e2e"}`,
			filepath.Join(repo, "x.go"),
		)
		return runHookCheckForTest(t, dataDir, input)
	}
	for i := 1; i <= 2; i++ {
		resp := run(i)
		if resp["continue"] != true {
			t.Fatalf("event %d: continue = %v, want true", i, resp["continue"])
		}
		if _, has := resp["systemMessage"]; has {
			t.Fatalf("event %d: unexpected systemMessage below threshold: %v", i, resp)
		}
	}
	resp := run(3)
	if resp["continue"] != true {
		t.Fatalf("advisory must not block: %v", resp)
	}
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, "pincher index") {
		t.Fatalf("third event systemMessage should recommend `pincher index`; got %v", resp)
	}
	resp = run(4)
	if _, has := resp["systemMessage"]; has {
		t.Fatalf("fourth event must be silent (once per root per session): %v", resp)
	}
}

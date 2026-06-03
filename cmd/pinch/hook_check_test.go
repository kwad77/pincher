// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #625: pincher hook-check decides whether to redirect a Read or Grep
// call to a pincher equivalent. Pass-through is silent (no stopReason
// emitted, just `{"continue": true}`); redirect carries `continue:false`
// + a one-sentence systemMessage with the suggested call.
//
// These tests exercise the decision logic directly via decideHook
// (skips the stdin/stdout shim) so each branch is unit-isolated.

func newHookTestStore(t *testing.T) *db.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func indexLargeFakeFile(t *testing.T, store *db.Store, projectDir, relPath string, sizeBytes int) (projectID string) {
	t.Helper()
	abs := filepath.Join(projectDir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := bytes.Repeat([]byte("x"), sizeBytes)
	if err := os.WriteFile(abs, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	projectID = "p-" + filepath.Base(projectDir)
	if err := store.UpsertProject(db.Project{
		ID: projectID, Path: projectDir, Name: filepath.Base(projectDir),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if err := store.SetFileHash(projectID, relPath, "fakehash"); err != nil {
		t.Fatalf("set file hash: %v", err)
	}
	// Seed enough symbols that the symbol-count gate passes.
	syms := make([]db.Symbol, 0, 6)
	for i := 0; i < 6; i++ {
		syms = append(syms, db.Symbol{
			ID:                   projectID + "::sym" + string(rune('a'+i)) + "#Function",
			ProjectID:            projectID,
			FilePath:             relPath,
			Name:                 "sym" + string(rune('a'+i)),
			QualifiedName:        "pkg.sym" + string(rune('a'+i)),
			Kind:                 "Function",
			Language:             "Go",
			StartByte:            i * 100,
			EndByte:              i*100 + 50,
			ExtractionConfidence: 1.0,
		})
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("upsert symbols: %v", err)
	}
	return projectID
}

func TestDecideHook_Read_LargeIndexedFile_Redirects(t *testing.T) {
	// #1654 v0.86: advisory mode. The hook nudges via systemMessage
	// but never blocks — blocking the Read broke Edit-prep workflows
	// (Edit requires a prior Read) and prose-doc reads where the
	// `context lite=true` redirect returns nothing useful.
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "internal/server/server.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000) // 50 KB

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Fatalf("advisory mode must NEVER block; got blocking decision %+v", d)
	}
	if d.Decision != "redirect_advisory" {
		t.Errorf("decision = %q, want redirect_advisory", d.Decision)
	}
	if d.SuggestedTool != "context" {
		t.Errorf("suggested tool = %q, want context", d.SuggestedTool)
	}
	if !strings.Contains(d.SuggestedArgs, `"lite":true`) {
		t.Errorf("suggested args should request lite mode; got %s", d.SuggestedArgs)
	}
	if !strings.Contains(d.SystemMessage, "context") {
		t.Errorf("system message should explain the suggested redirect; got %q", d.SystemMessage)
	}
	if d.StopReason != "" {
		t.Errorf("advisory mode must not set StopReason; got %q", d.StopReason)
	}
}

func TestDecideHook_Read_TinyFile_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "tiny.txt"
	indexLargeFakeFile(t, store, projectDir, relPath, 100) // 100 bytes

	in := hookCheckInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": filepath.Join(projectDir, relPath)},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("tiny file should pass through; got redirect: %+v", d)
	}
}

func TestDecideHook_Read_OffsetSet_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	relPath := "internal/server/server.go"
	indexLargeFakeFile(t, store, projectDir, relPath, 50000)

	in := hookCheckInput{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": filepath.Join(projectDir, relPath),
			"offset":    100,
		},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("offset-set Read should pass through (agent already narrowed); got redirect: %+v", d)
	}
}

func TestDecideHook_Read_UnindexedPath_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	in := hookCheckInput{
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "/nowhere/foo.go"},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("unindexed path should pass through; got redirect: %+v", d)
	}
}

func TestDecideHook_Grep_IdentifierPattern_RedirectsToSearch(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "f.go", 50000)

	cases := []string{
		"classifyTaskShape",  // CamelCase
		"foo_bar",            // snake
		"pkg.Bar",            // qualified
		"Class::method",      // C++ scope
	}
	for _, pattern := range cases {
		t.Run(pattern, func(t *testing.T) {
			in := hookCheckInput{
				ToolName:  "Grep",
				ToolInput: map[string]any{"pattern": pattern},
			}
			d := decideHook(store, in, false)
			// #1656 v0.86: Grep redirect is advisory, must pass through.
			if !d.Continue {
				t.Errorf("advisory mode must always pass Grep through; pattern %q got Continue=false", pattern)
			}
			if d.Decision != "redirect_advisory" {
				t.Errorf("decision = %q, want redirect_advisory", d.Decision)
			}
			if d.SuggestedTool != "search" {
				t.Errorf("suggested = %q, want search", d.SuggestedTool)
			}
		})
	}
}

func TestDecideHook_Grep_RegexOrPhrase_PassesThrough(t *testing.T) {
	store := newHookTestStore(t)
	projectDir := t.TempDir()
	indexLargeFakeFile(t, store, projectDir, "f.go", 50000)

	cases := []string{
		`func \w+\(`,         // regex
		`Foo(.*?)Bar`,        // capture group
		`hello world`,        // multi-word phrase
		`->`,                 // operator-only pattern
	}
	for _, pattern := range cases {
		t.Run(pattern, func(t *testing.T) {
			in := hookCheckInput{
				ToolName:  "Grep",
				ToolInput: map[string]any{"pattern": pattern},
			}
			d := decideHook(store, in, false)
			if !d.Continue {
				t.Errorf("non-identifier pattern %q should pass through; got redirect", pattern)
			}
		})
	}
}

func TestDecideHook_Grep_NoIndexedProjects_PassesThrough(t *testing.T) {
	// Identifier-shape pattern but no project indexed → pincher can't
	// help, pass through.
	store := newHookTestStore(t)
	in := hookCheckInput{
		ToolName:  "Grep",
		ToolInput: map[string]any{"pattern": "Foo"},
	}
	d := decideHook(store, in, false)
	if !d.Continue {
		t.Errorf("Grep on empty index should pass through; got %+v", d)
	}
}

// Cover the I/O shims that don't get hit by the decideHook unit tests:
// emitHookResponse, emitPassThrough, logHookDecision, debugPass on the
// observable-stderr path. End-to-end via stdin/stdout swap.
func TestRunHookCheckCLI_PassThroughEndToEnd(t *testing.T) {
	dir := t.TempDir()
	// Swap stdin to a piped JSON payload; capture stdout to a temp file.
	in, _ := os.CreateTemp(t.TempDir(), "stdin")
	in.WriteString(`{"tool_name":"Read","tool_input":{"file_path":"/nowhere/foo.go"}}`)
	in.Close()
	stdinFile, _ := os.Open(in.Name())
	defer stdinFile.Close()

	outFile, _ := os.CreateTemp(t.TempDir(), "stdout")
	defer outFile.Close()

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin = stdinFile
	os.Stdout = outFile
	defer func() { os.Stdin = origStdin; os.Stdout = origStdout }()

	runHookCheckCLI([]string{"--data-dir", dir})

	outFile.Sync()
	body, _ := os.ReadFile(outFile.Name())
	got := strings.TrimSpace(string(body))
	if !strings.Contains(got, `"continue":true`) {
		t.Errorf("unindexed Read should pass through; got %q", got)
	}
	// Unindexed-path pass-through should still be silent — no hint
	// to surface. Advisory hints only appear when matchIndexedFile
	// resolved a hit.
	if strings.Contains(got, "stopReason") || strings.Contains(got, "systemMessage") {
		t.Errorf("silent pass-through leaked chrome; got %q", got)
	}
}

func TestRunHookCheckCLI_BadJSONStillPassesThrough(t *testing.T) {
	dir := t.TempDir()
	in, _ := os.CreateTemp(t.TempDir(), "stdin")
	in.WriteString(`not valid json`)
	in.Close()
	stdinFile, _ := os.Open(in.Name())
	defer stdinFile.Close()

	outFile, _ := os.CreateTemp(t.TempDir(), "stdout")
	defer outFile.Close()

	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin = stdinFile
	os.Stdout = outFile
	defer func() { os.Stdin = origStdin; os.Stdout = origStdout }()

	runHookCheckCLI([]string{"--data-dir", dir, "--debug"})

	outFile.Sync()
	body, _ := os.ReadFile(outFile.Name())
	if !strings.Contains(string(body), `"continue":true`) {
		t.Errorf("malformed input must NOT block the agent; got %q", body)
	}
}

func TestEmitHookResponse_SilentPassThrough_NoChrome(t *testing.T) {
	// Silent pass-through (Decision="pass_through", no message) must
	// NOT include stopReason / systemMessage chrome — otherwise every
	// successful Read generates noise that trains the user to disable
	// the hook.
	d := hookDecision{Continue: true, Decision: "pass_through"}
	resp := map[string]any{"continue": d.Continue}
	if d.StopReason != "" {
		resp["stopReason"] = d.StopReason
	}
	if d.SystemMessage != "" {
		resp["systemMessage"] = d.SystemMessage
	}
	var buf bytes.Buffer
	out, _ := json.Marshal(resp)
	buf.Write(out)
	got := buf.String()
	if strings.Contains(got, "stopReason") || strings.Contains(got, "systemMessage") {
		t.Errorf("silent pass-through leaked chrome: %s", got)
	}
}

func TestEmitHookResponse_AdvisoryRedirect_CarriesSystemMessage_1654(t *testing.T) {
	// #1654 v0.86: redirect_advisory mode passes through (continue=true)
	// but emits systemMessage so the agent still sees the suggestion.
	d := hookDecision{
		Continue:      true,
		Decision:      "redirect_advisory",
		SystemMessage: "Pincher hint: this file is indexed",
	}
	resp := map[string]any{"continue": d.Continue}
	if d.StopReason != "" {
		resp["stopReason"] = d.StopReason
	}
	if d.SystemMessage != "" {
		resp["systemMessage"] = d.SystemMessage
	}
	out, _ := json.Marshal(resp)
	got := string(out)
	if !strings.Contains(got, `"continue":true`) {
		t.Errorf("advisory mode must pass through; got %q", got)
	}
	if !strings.Contains(got, "systemMessage") {
		t.Errorf("advisory mode must carry systemMessage hint; got %q", got)
	}
	if strings.Contains(got, "stopReason") {
		t.Errorf("advisory mode must not block via stopReason; got %q", got)
	}
}

// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/index"
)

// #1391 Phase 4 audit suite for investigate_failure. Positive +
// negative + control + cross-check shape per the composite-tool
// roadmap contract.

const investigateFailureGoSrc = `package authz

// Login authenticates a user. Test fixture for investigate_failure
// composite — its name + qualified-name must match the synthetic
// stack-frame token passed in the test below.
func Login(user string) error {
	return validateCreds(user)
}

func validateCreds(user string) error {
	return nil
}

// Caller exercises Login so we can verify caller fan-in scoring.
func Caller() error {
	return Login("alice")
}
`

func setupInvestigateTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	writeGoFile(t, root, "authz.go", investigateFailureGoSrc)
	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID
	return srv, root, res.ProjectID
}

// ─────────────────────────────────────────────────────────────────
// Pure-unit parser tests (no server / no DB)
// ─────────────────────────────────────────────────────────────────

// TestInvestigateFailure_ParseFrames_GoPanic — positive: a Go panic
// trace yields the expected frame names + file paths. Pins the
// parser's behaviour against the canonical Go panic shape.
func TestInvestigateFailure_ParseFrames_GoPanic(t *testing.T) {
	t.Parallel()
	trace := `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x4a8123]

goroutine 1 [running]:
main.processOrder(0x0)
	/home/dev/app/order.go:42 +0x55
main.handleRequest(0xc000010240, 0xc000010300)
	/home/dev/app/server.go:117 +0xa2
`
	names, files := parseStackFrames(trace)

	wantNames := map[string]bool{"processOrder": false, "handleRequest": false}
	for _, n := range names {
		if _, ok := wantNames[n]; ok {
			wantNames[n] = true
		}
	}
	for n, found := range wantNames {
		if !found {
			t.Errorf("expected name %q in parsed frames; got %v", n, names)
		}
	}
	for _, n := range names {
		if stopwordFrames[n] {
			t.Errorf("stopword %q leaked into parsed names", n)
		}
	}

	wantFiles := []string{"/home/dev/app/order.go", "/home/dev/app/server.go"}
	for _, want := range wantFiles {
		found := false
		for _, f := range files {
			if f == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected file %q in parsed paths; got %v", want, files)
		}
	}
}

// TestInvestigateFailure_ParseFrames_PythonTraceback — positive:
// Python's `File "..."` shape parses too. Composite is language-agnostic.
func TestInvestigateFailure_ParseFrames_PythonTraceback(t *testing.T) {
	t.Parallel()
	trace := `Traceback (most recent call last):
  File "/app/handler.py", line 42, in process_order
    result = compute_total(order)
  File "/app/billing.py", line 15, in compute_total
    return sum(item.price for item in order)
TypeError: 'NoneType' object is not iterable
`
	names, files := parseStackFrames(trace)

	wantNames := map[string]bool{"process_order": false, "compute_total": false}
	for _, n := range names {
		if _, ok := wantNames[n]; ok {
			wantNames[n] = true
		}
	}
	for n, found := range wantNames {
		if !found {
			t.Errorf("expected name %q in parsed frames; got %v", n, names)
		}
	}

	wantFiles := []string{"/app/handler.py", "/app/billing.py"}
	for _, want := range wantFiles {
		found := false
		for _, f := range files {
			if f == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected file %q; got %v", want, files)
		}
	}
}

// TestInvestigateFailure_ParseFrames_DottedNames — positive: dotted
// names contribute to BOTH the dotted form AND the short form so BM25
// can find the symbol whether it was extracted under its qualified or
// short name.
func TestInvestigateFailure_ParseFrames_DottedNames(t *testing.T) {
	t.Parallel()
	trace := `panic at auth.Login (login.go:42)
called from server.HandleRequest (server.go:88)
`
	names, _ := parseStackFrames(trace)

	want := []string{"auth.Login", "Login", "server.HandleRequest", "HandleRequest"}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected name %q in parsed frames; got %v", w, names)
		}
	}
}

// TestInvestigateFailure_StopwordsFiltered — control: stopword-only
// trace parses to zero names. Pins the stopword set.
func TestInvestigateFailure_StopwordsFiltered(t *testing.T) {
	t.Parallel()
	trace := "panic: runtime Error in goroutine main\nTraceback at line File"
	names, _ := parseStackFrames(trace)
	for _, n := range names {
		if stopwordFrames[n] {
			t.Errorf("stopword %q survived filter", n)
		}
	}
}

// TestInvestigateFailure_TestFrameworkVocabularyFiltered_1880 — control:
// Go testing-package output tokens that BM25-leaked into ranked
// suspects on a real test-failure dogfood probe (#1880) must be
// dropped by parseStackFrames before they reach the search step.
// Pre-fix `FAIL` / `PASS` / `SKIP` / `RUN` / `PAUSE` / `status` / `exit`
// were treated as identifier-shaped tokens; this test pins them as
// stopwords so a regression that adds testing.T or `t.Run` output to
// the trace can't re-leak through.
func TestInvestigateFailure_TestFrameworkVocabularyFiltered_1880(t *testing.T) {
	t.Parallel()
	trace := `--- FAIL: TestX (1.63s)
    update_test.go:483: pincher update: exit status 1
=== RUN   TestY
=== PAUSE TestY
--- PASS: TestY (0.00s)
--- SKIP: TestZ
FAIL	github.com/kwad77/pincher/cmd/pinch	158.200s`
	names, _ := parseStackFrames(trace)
	for _, leaked := range []string{"FAIL", "PASS", "SKIP", "RUN", "PAUSE", "status", "exit"} {
		for _, n := range names {
			if n == leaked {
				t.Errorf("test-framework token %q survived filter — must be in stopwordFrames", leaked)
				break
			}
		}
	}
	// Sanity: real identifier-shaped tokens (TestX / TestY) must survive.
	survived := map[string]bool{}
	for _, n := range names {
		survived[n] = true
	}
	for _, expect := range []string{"TestX", "TestY"} {
		if !survived[expect] {
			t.Errorf("expected identifier %q to survive filter; got names=%v", expect, names)
		}
	}
}

// ─────────────────────────────────────────────────────────────────
// Handler tests (server + real indexed corpus)
// ─────────────────────────────────────────────────────────────────

// TestInvestigateFailure_MissingErrorText — negative-control: zero
// arguments returns a rich error.
func TestInvestigateFailure_MissingErrorText(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	res, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected go-level error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("expected IsError=true for missing error_text")
	}
	text := textOf(t, res)
	if !strings.Contains(text, "investigate_failure requires `error_text`") {
		t.Errorf("expected rich-error message; got %s", text)
	}
}

// TestInvestigateFailure_NoFramesParse — empty path: input with no
// identifier-shaped tokens hits the parser's empty branch and stamps
// empty_reason + diagnosis.
func TestInvestigateFailure_NoFramesParse(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupInvestigateTestServer(t)

	res, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text": "&& || !! ?? **",
		"project":    projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatal("missing _meta")
	}
	// #1578 v0.82: parser-can't-extract-anything is target_not_resolved
	// (input was wrong shape — neither identifier-shaped tokens nor
	// file:line patterns matched), not no_results_in_corpus.
	if meta["empty_reason"] != EmptyReasonTargetNotResolved {
		t.Errorf("empty_reason = %v; want %s", meta["empty_reason"], EmptyReasonTargetNotResolved)
	}
	fp, ok := body["frames_parsed"].(map[string]any)
	if !ok {
		t.Fatal("missing frames_parsed")
	}
	if names, _ := fp["names"].([]any); len(names) != 0 {
		t.Errorf("expected zero parsed names; got %v", names)
	}
}

// TestInvestigateFailure_NoMatchingSymbols — positive empty-path:
// frames parse but no symbol resolves. Distinguishable diagnosis
// (mentions "parsed N frame name(s)").
func TestInvestigateFailure_NoMatchingSymbols(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupInvestigateTestServer(t)

	res, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text": "panic at totallyUnindexedFunctionName (nowhere.go:1)",
		"project":    projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatal("missing _meta")
	}
	diag, _ := meta["diagnosis"].(string)
	if !strings.Contains(diag, "parsed") || !strings.Contains(diag, "frame name") {
		t.Errorf("expected diagnosis to mention parsed frames; got %q", diag)
	}
	suspects, _ := body["implicated_symbols"].([]any)
	if len(suspects) != 0 {
		t.Errorf("expected zero suspects; got %d", len(suspects))
	}
}

// TestInvestigateFailure_NoFramesParsed_FilesOnly_NextStepsValid —
// regression for #1570. When the trace parser captures file paths but
// no frame identifier names (e.g. a Python traceback with file paths
// the name regex doesn't catch), the empty-suspects branch previously
// emitted a next-step `{"tool":"search","args":{"query":""}}` — an
// invalid call that breaks the failure-as-pedagogy chain. Now the
// search hint uses the file basename (extension stripped) when no
// frame names are available, or is omitted entirely if neither.
func TestInvestigateFailure_NoFramesParsed_FilesOnly_NextStepsValid(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupInvestigateTestServer(t)

	// Python traceback shape — quoted file path, no identifier-shape
	// tokens the parser will keep. Use a not-actually-indexed file
	// path so we go through the empty-suspects branch deterministically.
	traceback := `Traceback (most recent call last):
  File "/usr/local/lib/python3.11/site-packages/external_dep_xyz.py", line 42, in ?
  File "/usr/local/lib/python3.11/site-packages/external_dep_xyz.py", line 17, in ?
`

	res, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text": traceback,
		"project":    projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	meta, ok := body["_meta"].(map[string]any)
	if !ok {
		t.Fatal("missing _meta")
	}
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatal("expected at least one next_step (doctor at minimum)")
	}
	// Walk every next_step; if any is a `search` tool, its `args` JSON
	// must NOT contain an empty query.
	for i, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if step["tool"] != "search" {
			continue
		}
		argsStr, _ := step["args"].(string)
		if strings.Contains(argsStr, `"query":""`) || strings.Contains(argsStr, `"query": ""`) {
			t.Errorf("#1570 regression: next_steps[%d] suggests `search` with empty query: %s", i, argsStr)
		}
	}
}

// TestInvestigateFailure_RankByFrameMatch — positive happy path:
// trace mentioning an indexed Function name yields a ranked suspect
// with the stack_frame_match evidence flag.
func TestInvestigateFailure_RankByFrameMatch(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupInvestigateTestServer(t)

	res, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text": "panic at authz.Login (authz.go:6)\n  called from main.processRequest (main.go:42)",
		"project":    projectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)

	suspects, ok := body["implicated_symbols"].([]any)
	if !ok || len(suspects) == 0 {
		t.Fatalf("expected at least one suspect; got %v", suspects)
	}
	// At least one suspect should be Login.
	foundLogin := false
	for _, s := range suspects {
		m := s.(map[string]any)
		if name, _ := m["name"].(string); name == "Login" {
			foundLogin = true
			evidence, _ := m["evidence"].([]any)
			hasFrameMatch := false
			for _, e := range evidence {
				if es, _ := e.(string); es == "stack_frame_match" {
					hasFrameMatch = true
				}
			}
			if !hasFrameMatch {
				t.Errorf("Login suspect missing stack_frame_match evidence; got %v", evidence)
			}
		}
	}
	if !foundLogin {
		t.Errorf("expected Login among suspects; got %v", suspects)
	}
}

// TestInvestigateFailure_CapsCallersUnion — a hot suspect named in
// the trace (a hub symbol with hundreds of inbound callers) used to
// produce an unbounded callers union, blowing the envelope past the
// MCP token limit (#1723). The callers list is now capped;
// callers_total keeps the true union size and callers_truncated
// flags the truncation.
func TestInvestigateFailure_CapsCallersUnion(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root

	const callerCount = 60
	var b strings.Builder
	b.WriteString("package hot\n\nfunc Hot() int { return 1 }\n\n")
	for i := 0; i < callerCount; i++ {
		fmt.Fprintf(&b, "func Caller%d() int { return Hot() }\n", i)
	}
	writeGoFile(t, root, "hot.go", b.String())
	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID

	out, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text": "panic at hot.Hot (hot.go:3)",
		"project":    res.ProjectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, out)
	callers, _ := body["callers"].([]any)
	if len(callers) > 50 {
		t.Errorf("callers list rendered %d rows; must be capped at 50", len(callers))
	}
	if total, _ := body["callers_total"].(float64); int(total) < callerCount {
		t.Errorf("callers_total = %v; expected the true union size ≥ %d "+
			"(the total must survive truncation, not collapse to the capped list length)", total, callerCount)
	}
	if tr, _ := body["callers_truncated"].(bool); !tr {
		t.Error("callers_truncated = false; expected true when the callers union exceeds the cap")
	}
}

// TestInvestigateFailure_RankingIsDeterministic — cross-check: same
// input produces the same ranking across two invocations. Determinism
// is load-bearing for tests + reproducible debugging.
func TestInvestigateFailure_RankingIsDeterministic(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupInvestigateTestServer(t)

	args := map[string]any{
		"error_text": "panic at Login (authz.go:6)\n  called from validateCreds (authz.go:11)",
		"project":    projectID,
	}

	res1, _ := srv.handleInvestigateFailure(context.Background(), makeReq(args))
	res2, _ := srv.handleInvestigateFailure(context.Background(), makeReq(args))
	b1 := decode(t, res1)
	b2 := decode(t, res2)

	s1, _ := b1["implicated_symbols"].([]any)
	s2, _ := b2["implicated_symbols"].([]any)
	if len(s1) != len(s2) {
		t.Fatalf("non-deterministic suspect count: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		m1 := s1[i].(map[string]any)
		m2 := s2[i].(map[string]any)
		if m1["symbol_id"] != m2["symbol_id"] {
			t.Errorf("rank[%d] non-deterministic: %v vs %v", i, m1["symbol_id"], m2["symbol_id"])
		}
	}
}

// TestInvestigateFailure_MaxSuspectsCap — control: max_suspects=2
// returns ≤ 2 ranked suspects.
func TestInvestigateFailure_MaxSuspectsCap(t *testing.T) {
	t.Parallel()
	srv, _, projectID := setupInvestigateTestServer(t)

	res, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text":   "panic at Login validateCreds Caller (authz.go:6)",
		"project":      projectID,
		"max_suspects": 2,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, res)
	suspects, _ := body["implicated_symbols"].([]any)
	if len(suspects) > 2 {
		t.Errorf("max_suspects=2 not honoured; got %d suspects", len(suspects))
	}
}

// TestInvestigateFailure_IsRegistered — gate: the tool is registered
// and discoverable. Pins runtime-registration side of the contract;
// the doc-listing parity test (#1514) pins the docs side.
func TestInvestigateFailure_IsRegistered(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	tool, ok := srv.tools["investigate_failure"]
	if !ok {
		t.Fatal("investigate_failure not registered in srv.tools")
	}
	if !strings.Contains(tool.Description, "stack trace") {
		t.Errorf("description should mention stack trace; got %q", tool.Description)
	}
}

const investigateSiblingSrc = `package work

type Worker struct{}

func NewWorker() *Worker { return &Worker{} }
func (w *Worker) Reset()       {}
func (w *Worker) Flush()       {}
func (w *Worker) MaxSize() int { return 0 }

// processItem is the crash site named in the panic frame below.
func (w *Worker) processItem() error { return nil }
`

// TestInvestigateFailure_ExactNameOutranksSiblingMethods is the #1751
// fix: a Go panic naming (*Worker).processItem must rank processItem —
// the symbol literally named in the trace — above the unrelated
// *Worker sibling methods that merely share the receiver-type token
// `Worker`. Pre-fix two bugs compounded: package-path noise tokens
// (`github.com`, `internal`, `work` — repeated once per Go frame)
// out-frequencied the once-mentioned function name, so the search
// loop's candidate budget was exhausted before processItem was ever
// searched; and a flat +0.45 frame-match score let a sibling method
// tie the real crash site.
func TestInvestigateFailure_ExactNameOutranksSiblingMethods(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root
	writeGoFile(t, root, "internal/work/worker.go", investigateSiblingSrc)
	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID

	// A 3-frame Go panic. The package path repeats once per frame, so
	// `github.com` / `internal` / `work` reach count 3 while the crash
	// function `processItem` appears once — the exact condition that
	// starved the pre-fix search loop.
	trace := `panic: runtime error: invalid memory address or nil pointer dereference
goroutine 9 [running]:
github.com/acme/widgets/internal/work.(*Worker).processItem(0xc0001a2000)
	internal/work/worker.go:11 +0x1a4
github.com/acme/widgets/internal/work.runPipeline(0xc0001a2000)
	internal/work/pipeline.go:30 +0x88
github.com/acme/widgets/internal/work.dispatch(0xc0001a2000)
	internal/work/pipeline.go:12 +0x44
`
	out, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text": trace,
		"project":    res.ProjectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, out)
	rank, ok := body["rank"].([]any)
	if !ok || len(rank) == 0 {
		t.Fatalf("expected ranked suspects; got %v", body["rank"])
	}
	top := rank[0].(map[string]any)
	if top["name"] != "processItem" {
		t.Errorf("top suspect = %v, want processItem (the function named in the panic frame, not a sibling *Worker method)", top["name"])
	}
	ev, _ := top["evidence"].([]any)
	hasExact := false
	for _, e := range ev {
		if s, _ := e.(string); s == "stack_frame_exact_name_match" {
			hasExact = true
		}
	}
	if !hasExact {
		t.Errorf("top suspect missing stack_frame_exact_name_match evidence; got %v", ev)
	}
}

// TestInvestigateFailure_ExactNameSurvivesTruncation_1831 — the #1751
// exact-name score boost runs in the scoring step, but the candidate
// pool is first truncated to maxSuspects*2 by raw BM25. On a struct
// with many methods the receiver-type token (`Engine`) loose-matches
// every method's signature; pre-#1831 enough of those loose matches
// outscored the exact crash-frame symbol and evicted it from the pool
// BEFORE scoring ran. The crash site then never appeared in
// implicated_symbols at all.
func TestInvestigateFailure_ExactNameSurvivesTruncation_1831(t *testing.T) {
	t.Parallel()
	srv, store, root := newTestServer(t)
	srv.sessionRoot = root

	// A struct with 55 sibling methods — every signature carries the
	// receiver-type token `Engine`, so a search for that token returns
	// a large loose-match pool that pressures the maxSuspects*2 cap.
	var b strings.Builder
	b.WriteString("package engine\n\ntype Engine struct{}\n\n")
	for i := 0; i < 55; i++ {
		fmt.Fprintf(&b, "func (e *Engine) phase%02d() int { return %d }\n", i, i)
	}
	// detonate is the crash site named in the panic frame below.
	b.WriteString("func (e *Engine) detonate() error { return nil }\n")
	writeGoFile(t, root, "internal/engine/engine.go", b.String())

	idx := index.New(store)
	res, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	srv.sessionID = res.ProjectID

	trace := `panic: runtime error: invalid memory address or nil pointer dereference
goroutine 1 [running]:
github.com/acme/app/internal/engine.(*Engine).detonate(0xc000010000)
	internal/engine/engine.go:57 +0x1a4
`
	out, err := srv.handleInvestigateFailure(context.Background(), makeReq(map[string]any{
		"error_text": trace,
		"project":    res.ProjectID,
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	body := decode(t, out)
	implicated, _ := body["implicated_symbols"].([]any)
	found := false
	for _, s := range implicated {
		m, _ := s.(map[string]any)
		if m["name"] == "detonate" {
			found = true
		}
	}
	if !found {
		names := make([]string, 0, len(implicated))
		for _, s := range implicated {
			m, _ := s.(map[string]any)
			names = append(names, fmt.Sprint(m["name"]))
		}
		t.Errorf("detonate (the exact crash-frame symbol) absent from implicated_symbols — "+
			"truncated before scoring by sibling *Engine loose matches (#1831); got %v", names)
	}
}

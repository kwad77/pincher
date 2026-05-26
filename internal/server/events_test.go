package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #654: GET /v1/events Server-Sent Events stream. These tests pin:
//   - SSE headers + the binary_drift snapshot on connect
//   - live index_started / index_complete events when an index runs
//   - the ?project= subscriber filter
//   - --http-key bearer enforcement
//   - non-GET → 405
//   - the `sse` capability advertisement
//   - concurrent subscribers all receive the same event (no shared-state
//     corruption — the #654 acceptance item)

// sseFrame is one parsed SSE event: the `event:` name and the decoded
// `data:` JSON object.
type sseFrame struct {
	event string
	data  map[string]any
}

// readSSEFrames reads up to `want` SSE frames from r, or returns
// whatever it got when `timeout` elapses. Keepalive comment lines
// (": …") are skipped. Used instead of a raw scanner so tests assert on
// structured events, not line soup.
func readSSEFrames(t *testing.T, r *http.Response, want int, timeout time.Duration) []sseFrame {
	t.Helper()
	frames := make([]sseFrame, 0, want)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(r.Body)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.data)
			case line == "" && cur.event != "":
				frames = append(frames, cur)
				cur = sseFrame{}
				if len(frames) >= want {
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		// Timeout: the reader goroutine is still scanning and
		// appending to `frames`. Returning the slice now races the
		// goroutine's writes against the caller's reads (the `-race`
		// CI job, #1733, flagged exactly this). Close the body to
		// unblock the scanner — sc.Scan() then returns false and the
		// goroutine exits — and wait for `done` so `frames` is
		// quiescent before we return it.
		_ = r.Body.Close()
		<-done
	}
	return frames
}

// writeIndexableRepo drops a minimal Go file into a fresh temp dir so
// idx.Index has something to extract.
func writeIndexableRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := "package demo\n\nfunc Demo() int { return 42 }\n"
	if err := os.WriteFile(filepath.Join(dir, "demo.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	return dir
}

func waitForEventSubscribers(t *testing.T, srv *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if srv.events.subscriberCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event subscribers = %d, want at least %d", srv.events.subscriberCount(), want)
}

func TestEvents_DriftSnapshotOnConnect(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.version = "0.56.0-test"
	// A project indexed by a different binary version → drifted.
	if err := store.UpsertProject(db.Project{
		ID: "driftpr", Path: "/tmp/driftpr", Name: "driftpr",
		IndexedAt: time.Now(), BinaryVersion: "0.40.0-old",
	}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/events")
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	frames := readSSEFrames(t, resp, 1, 2*time.Second)
	if len(frames) == 0 {
		t.Fatal("no SSE frame received — expected a binary_drift snapshot on connect")
	}
	f := frames[0]
	if f.event != "binary_drift" {
		t.Errorf("first frame event = %q, want binary_drift", f.event)
	}
	if f.data["project_id"] != "driftpr" {
		t.Errorf("binary_drift project_id = %v, want driftpr", f.data["project_id"])
	}
	if f.data["indexed_version"] != "0.40.0-old" || f.data["running_version"] != "0.56.0-test" {
		t.Errorf("drift versions wrong: %v", f.data)
	}
}

func TestEvents_IndexLifecycleStreamed(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	repo := writeIndexableRepo(t)
	projectID := db.ProjectIDFromPath(repo)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Filter to our repo so the session-root auto-index (if any) can't
	// pollute the assertion.
	resp, err := http.Get(ts.URL + "/v1/events?project=" + projectID)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()
	waitForEventSubscribers(t, srv, 1)

	_, _ = srv.indexer.Index(context.Background(), repo, false)

	frames := readSSEFrames(t, resp, 2, 5*time.Second)
	got := map[string]map[string]any{}
	for _, f := range frames {
		got[f.event] = f.data
	}
	if _, ok := got["index_started"]; !ok {
		t.Errorf("did not receive index_started; got events %v", frames)
	}
	if done, ok := got["index_complete"]; !ok {
		t.Errorf("did not receive index_complete; got events %v", frames)
	} else {
		if done["project_id"] != projectID {
			t.Errorf("index_complete project_id = %v, want %v", done["project_id"], projectID)
		}
		if syms, _ := done["symbols"].(float64); syms < 1 {
			t.Errorf("index_complete symbols = %v, want >= 1 (the Demo func)", done["symbols"])
		}
	}
}

func TestEvents_ProjectFilterExcludesOthers(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.version = "0.56.0-test"
	// Two drifted projects; the subscriber asks for only one.
	for _, id := range []string{"keep", "drop"} {
		if err := store.UpsertProject(db.Project{
			ID: id, Path: "/tmp/" + id, Name: id,
			IndexedAt: time.Now(), BinaryVersion: "0.40.0-old",
		}); err != nil {
			t.Fatal(err)
		}
	}

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/events?project=keep")
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp, 2, 100*time.Millisecond)
	for _, f := range frames {
		if pid, _ := f.data["project_id"].(string); pid != "keep" {
			t.Errorf("project filter leaked an event for %q: %v", pid, f)
		}
	}
	if len(frames) == 0 {
		t.Error("expected the snapshot for project=keep, got nothing")
	}
}

func TestEvents_RequiresAuthWhenKeySet(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.SetHTTPKey("sse-secret")

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// No bearer → 401.
	resp, err := http.Get(ts.URL + "/v1/events")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-bearer status = %d, want 401", resp.StatusCode)
	}

	// With bearer → 200 + event-stream.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer sse-secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authed GET: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("authed status = %d, want 200", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("authed Content-Type = %q, want text/event-stream", ct)
	}
}

func TestEvents_NonGetRejected(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/events", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/events status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "GET" {
		t.Errorf("Allow header = %q, want GET", allow)
	}
}

func TestEvents_CapabilityAdvertised(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	found := false
	for _, c := range srv.capabilities {
		if c == "sse" {
			found = true
		}
	}
	if !found {
		t.Errorf("capabilities missing \"sse\"; got %v", srv.capabilities)
	}
}

func TestEvents_OpenAPIDeclaresEndpoint(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	spec := srv.openAPISpec(httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	paths, _ := spec["paths"].(map[string]any)
	ev, ok := paths["/v1/events"].(map[string]any)
	if !ok {
		t.Fatalf("openapi paths missing /v1/events; got keys %v", paths)
	}
	get, _ := ev["get"].(map[string]any)
	if get["operationId"] != "events" {
		t.Errorf("/v1/events operationId = %v, want events", get["operationId"])
	}
	resps, _ := get["responses"].(map[string]any)
	ok200, _ := resps["200"].(map[string]any)
	content, _ := ok200["content"].(map[string]any)
	if _, ok := content["text/event-stream"]; !ok {
		t.Errorf("/v1/events 200 response not declared as text/event-stream: %v", content)
	}
}

// #654 acceptance: multiple concurrent subscribers must all receive the
// same event with no shared-state corruption.
func TestEvents_ConcurrentSubscribers(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	repo := writeIndexableRepo(t)
	projectID := db.ProjectIDFromPath(repo)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	const subscribers = 5
	resps := make([]*http.Response, subscribers)
	for i := range resps {
		r, err := http.Get(ts.URL + "/v1/events?project=" + projectID)
		if err != nil {
			t.Fatalf("subscriber %d connect: %v", i, err)
		}
		resps[i] = r
		defer r.Body.Close()
	}
	waitForEventSubscribers(t, srv, subscribers)

	var wg sync.WaitGroup
	results := make([]bool, subscribers)
	for i := range resps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			frames := readSSEFrames(t, resps[i], 2, 5*time.Second)
			for _, f := range frames {
				if f.event == "index_complete" && f.data["project_id"] == projectID {
					results[i] = true
				}
			}
		}(i)
	}
	_, _ = srv.indexer.Index(context.Background(), repo, false)
	wg.Wait()

	for i, ok := range results {
		if !ok {
			t.Errorf("subscriber %d did not receive index_complete", i)
		}
	}
}

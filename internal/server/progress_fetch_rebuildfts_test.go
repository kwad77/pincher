// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kwad77/pincher/internal/db"
)

// #1950: fetch and rebuild_fts join index on the notifications/progress
// surface (#1080). These tests drive the real handlers end-to-end over
// the in-memory MCP transport and assert on the terminal progress event
// (mid-stream ticks race the 400ms poll cadence on fast operations; the
// final emit is deterministic).

// reqWithTokenAndArgs builds a CallToolRequest carrying both tool
// arguments and a progressToken. See reqWithProgressToken for why Meta
// must be non-nil before SetProgressToken.
func reqWithTokenAndArgs(token any, name string, args map[string]any) *mcp.CallToolRequest {
	b, _ := json.Marshal(args)
	p := &mcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(b), Meta: mcp.Meta{}}
	p.SetProgressToken(token)
	return &mcp.CallToolRequest{Params: p}
}

// collectProgress connects an in-memory client whose handler appends
// every progress notification to the returned slice (guarded by mu).
func collectProgress(t *testing.T, srv *Server) (*sync.Mutex, *[]*mcp.ProgressNotificationParams) {
	t.Helper()
	var mu sync.Mutex
	var recv []*mcp.ProgressNotificationParams
	handler := func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
		mu.Lock()
		recv = append(recv, req.Params)
		mu.Unlock()
	}
	_, cleanup := connectInMemoryClient(t, srv, &mcp.ClientOptions{
		ProgressNotificationHandler: handler,
	})
	t.Cleanup(cleanup)
	waitForSession(t, srv, 2*time.Second)
	return &mu, &recv
}

// waitForTerminal polls recv until a notification with the wanted
// progress+total arrives or the deadline passes.
func waitForTerminal(mu *sync.Mutex, recv *[]*mcp.ProgressNotificationParams, progress, total float64, d time.Duration) *mcp.ProgressNotificationParams {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, p := range *recv {
			if p.Progress == progress && p.Total == total {
				mu.Unlock()
				return p
			}
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func TestHandleFetch_EmitsProgressWithToken(t *testing.T) {
	srv, store := fetchTestSetup(t)
	_ = store
	srv.fetchAllowLoopback = true

	body := bytes.Repeat([]byte("pincher progress payload "), 2048)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Explicit Content-Length so fetch has a determinate total.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer upstream.Close()

	mu, recv := collectProgress(t, srv)

	result, err := srv.handleFetch(context.Background(),
		reqWithTokenAndArgs("tok-fetch", "fetch", fetchArgs(upstream.URL)))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if m := decode(t, result); m["stored"] != true {
		t.Fatalf("stored = %v, want true (result: %v)", m["stored"], m)
	}

	terminal := waitForTerminal(mu, recv, float64(len(body)), float64(len(body)), 2*time.Second)
	if terminal == nil {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("no terminal progress event (%d/%d bytes); got %d events", len(body), len(body), len(*recv))
	}
	if terminal.ProgressToken != "tok-fetch" {
		t.Errorf("progressToken = %v, want %q", terminal.ProgressToken, "tok-fetch")
	}
}

func TestHandleFetch_NoContentLengthStillCompletes(t *testing.T) {
	srv, _ := fetchTestSetup(t)
	srv.fetchAllowLoopback = true

	body := bytes.Repeat([]byte("indeterminate stream "), 1024)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Flush forces chunked transfer — ContentLength becomes -1
		// client-side, the indeterminate path.
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write(body)
	}))
	defer upstream.Close()

	mu, recv := collectProgress(t, srv)

	result, err := srv.handleFetch(context.Background(),
		reqWithTokenAndArgs("tok-chunked", "fetch", fetchArgs(upstream.URL)))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if m := decode(t, result); m["stored"] != true {
		t.Fatalf("stored = %v, want true", m["stored"])
	}

	// Indeterminate total: the final emit reports done for both fields
	// (runWithProgress substitutes done when total is unknown).
	terminal := waitForTerminal(mu, recv, float64(len(body)), float64(len(body)), 2*time.Second)
	if terminal == nil {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("no terminal progress event on chunked response; got %d events", len(*recv))
	}
}

func TestHandleRebuildFTS_EmitsProgressWithToken(t *testing.T) {
	srv, store, _ := newTestServer(t)
	if err := store.UpsertProject(db.Project{
		ID: "p", Path: "/p", Name: "proj", IndexedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	mu, recv := collectProgress(t, srv)

	result, err := srv.handleRebuildFTS(context.Background(),
		reqWithTokenAndArgs("tok-fts", "rebuild_fts", map[string]any{"confirm": true}))
	if err != nil {
		t.Fatalf("handleRebuildFTS: %v", err)
	}
	if m := decode(t, result); m["dry_run"] != false {
		t.Fatalf("dry_run = %v, want false (result: %v)", m["dry_run"], m)
	}

	stages := float64(db.RebuildFTSStages)
	terminal := waitForTerminal(mu, recv, stages, stages, 2*time.Second)
	if terminal == nil {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("no terminal progress event (%v/%v stages); got %d events", stages, stages, len(*recv))
	}
	if terminal.ProgressToken != "tok-fts" {
		t.Errorf("progressToken = %v, want %q", terminal.ProgressToken, "tok-fts")
	}
}

// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #1106: fetch's non-2xx response path used bare errResult — no
// next_steps, no diagnosis. Same shape of bug as #1063 (search project-
// not-found) / #1064 (changes project-not-found): the failure carried
// no remediation context, so agents had to guess what to do next.
// Now: status-class-aware error envelope with remediation steps.

func TestHandleFetch_HTTP404_RichEnvelope(t *testing.T) {
	t.Parallel()
	srv, _ := fetchTestSetup(t)
	srv.fetchAllowLoopback = true

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	res, err := srv.handleFetch(context.Background(), makeReq(fetchArgs(upstream.URL)))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on 404; got %s", textOf(t, res))
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "404") {
		t.Errorf("error must name the status code; got %q", errStr)
	}
	if !strings.Contains(errStr, "not found") {
		t.Errorf("404 error must include the status-class hint 'not found'; got %q", errStr)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("expected _meta envelope; got bare error: %v", body)
	}
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("expected at least one next_step for 404; got none")
	}
	step0, _ := steps[0].(map[string]any)
	if tool, _ := step0["tool"].(string); tool != "fetch" {
		t.Errorf("404 next_step should point at fetch with corrected URL; got tool=%q", tool)
	}
}

func TestHandleFetch_HTTP401_AuthHint(t *testing.T) {
	t.Parallel()
	srv, _ := fetchTestSetup(t)
	srv.fetchAllowLoopback = true

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer upstream.Close()

	res, err := srv.handleFetch(context.Background(), makeReq(fetchArgs(upstream.URL)))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on 401")
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "401") {
		t.Errorf("error must name the status code; got %q", errStr)
	}
	if !strings.Contains(errStr, "authentication") && !strings.Contains(errStr, "forbidden") {
		t.Errorf("401 error must include auth-class hint; got %q", errStr)
	}
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("expected next_step pointing at local docs corpus for 401")
	}
	step0, _ := steps[0].(map[string]any)
	if tool, _ := step0["tool"].(string); tool != "search" {
		t.Errorf("401 next_step should redirect to search with corpus=docs; got tool=%q", tool)
	}
}

func TestHandleFetch_HTTP500_TransientRetryHint(t *testing.T) {
	t.Parallel()
	srv, _ := fetchTestSetup(t)
	srv.fetchAllowLoopback = true

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	res, err := srv.handleFetch(context.Background(), makeReq(fetchArgs(upstream.URL)))
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on 500")
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "transient") {
		t.Errorf("5xx error must include transient-retry hint; got %q", errStr)
	}
}

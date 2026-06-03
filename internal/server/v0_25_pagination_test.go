// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// v0.25 contract tests for the dashboard API hardening batch
// (#530/#531/#532/#535/#536/#537). Each test pins one acceptance
// criterion from the issue body so a future regression fails the
// gate that was added to catch it.

// #530: GET /v1/projects accepts ?limit=&offset= with default 50, max
// 200, and returns {projects, total, has_more}. Empty-state and
// past-the-end requests must still return [] (not null) and has_more
// false.
func TestProjects_Pagination(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now()
	for i := 0; i < 75; i++ {
		err := store.UpsertProject(db.Project{
			ID:        fmt.Sprintf("p%03d", i),
			Path:      fmt.Sprintf("/tmp/p%03d", i),
			Name:      fmt.Sprintf("p%03d", i),
			IndexedAt: now.Add(-time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("seed project %d: %v", i, err)
		}
	}

	cases := []struct {
		name              string
		query             string
		wantCount         int
		wantTotal         int
		wantHasMore       bool
		wantStatus        int
		wantFirstProjects []string
	}{
		{
			name: "default-limit-50",
			query: "", wantCount: 50, wantTotal: 75, wantHasMore: true, wantStatus: 200,
		},
		{
			name: "limit-10", query: "?limit=10",
			wantCount: 10, wantTotal: 75, wantHasMore: true, wantStatus: 200,
		},
		{
			name: "limit-10-offset-70", query: "?limit=10&offset=70",
			wantCount: 5, wantTotal: 75, wantHasMore: false, wantStatus: 200,
		},
		{
			name: "offset-past-end", query: "?offset=9999",
			wantCount: 0, wantTotal: 75, wantHasMore: false, wantStatus: 200,
		},
		{
			name: "limit-clamp-above-200", query: "?limit=10000",
			wantCount: 75, wantTotal: 75, wantHasMore: false, wantStatus: 200,
		},
		{
			name: "negative-limit-falls-to-default", query: "?limit=-5",
			wantCount: 50, wantTotal: 75, wantHasMore: true, wantStatus: 200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httpGet(t, srv, "/v1/projects"+tc.query)
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d\nbody: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			var resp struct {
				Projects []map[string]any `json:"projects"`
				Total    int              `json:"total"`
				HasMore  bool             `json:"has_more"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
			}
			if len(resp.Projects) != tc.wantCount {
				t.Errorf("len(projects) = %d, want %d", len(resp.Projects), tc.wantCount)
			}
			if resp.Total != tc.wantTotal {
				t.Errorf("total = %d, want %d", resp.Total, tc.wantTotal)
			}
			if resp.HasMore != tc.wantHasMore {
				t.Errorf("has_more = %v, want %v", resp.HasMore, tc.wantHasMore)
			}
			// #334-class invariant: empty page must serialize as []
			// (not null) so the dashboard's `.map(...)` doesn't throw.
			if strings.Contains(w.Body.String(), `"projects":null`) {
				t.Errorf("projects serialized as null, want []\nbody: %s", w.Body.String())
			}
		})
	}
}

// #531: GET /v1/sessions accepts ?limit= (default 90, max 500). The
// server pulls (limit+offset) rows from the store, then windows.
func TestSessions_LimitParam(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	now := time.Now()
	for i := 0; i < 25; i++ {
		err := store.RecordSession(
			fmt.Sprintf("s-%03d", i),
			now.Add(-time.Duration(i)*time.Minute),
			int64(i), int64(i*10), int64(i*100), 0, "", 0, "{}",
		)
		if err != nil {
			t.Fatalf("seed session %d: %v", i, err)
		}
	}

	w := httpGet(t, srv, "/v1/sessions?limit=5")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		Sessions []map[string]any `json:"sessions"`
		Total    int              `json:"total"`
		HasMore  bool             `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 5 {
		t.Errorf("len(sessions) = %d, want 5", len(resp.Sessions))
	}
	// total is rows fetched (limit+offset), not absolute db count —
	// the issue tracks raising it to a real count separately.
	if !resp.HasMore && resp.Total > 5 {
		t.Errorf("has_more=false but total=%d > 5", resp.Total)
	}

	// Default limit is 90 — verify the default path still works.
	w = httpGet(t, srv, "/v1/sessions")
	if w.Code != 200 {
		t.Fatalf("default status %d", w.Code)
	}
}

// #535: POST /v1/index-progress returns started_at + elapsed_ms +
// files_per_sec + eta_ms alongside the existing fields. When no index
// is active for the project, the timing fields are null (not 0).
func TestIndexProgress_HasETAFields(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	w := httpPost(t, srv, "/v1/index-progress", `{"project":"never-indexed"}`)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"started_at", "elapsed_ms", "files_per_sec", "eta_ms"} {
		if _, ok := resp[k]; !ok {
			t.Errorf("missing v0.25 #535 field %q\nresp: %v", k, resp)
		}
	}
	// All ETA fields are nil for an inactive project (no in-memory
	// progress entry exists). Pre-fix they wouldn't be present at all.
	if resp["started_at"] != nil {
		t.Errorf("started_at should be nil for never-indexed project; got %v", resp["started_at"])
	}
}

// #536: GET /v1/health includes dashboard_version equal to the server
// version. JS bakes its own build version in at render time and polls
// health to detect "your tab is running stale JS against a newer server".
func TestHealth_HasDashboardVersion(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	w := httpGet(t, srv, "/v1/health")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["dashboard_version"]; !ok {
		t.Errorf("v0.25 #536: missing dashboard_version field\nresp: %v", resp)
	}
	// In this release dashboard_version equals version. They're carried
	// as separate fields so they can advance independently later.
	if resp["dashboard_version"] != resp["version"] {
		t.Errorf("dashboard_version=%v != version=%v (this release should match)",
			resp["dashboard_version"], resp["version"])
	}
}

// #537: every 4xx/5xx response from the HTTP gateway uses the
// standardized envelope `{error: {code, message, details?}}`. Sweep
// every reachable error path: bad-request shapes, not-found, method
// not allowed.
func TestErrorEnvelope_StandardizedAcrossEndpoints(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode string
	}{
		{name: "delete-projects-empty-id", method: "DELETE", path: "/v1/projects",
			body: `{"id":""}`, wantCode: "bad_request"},
		{name: "delete-projects-malformed", method: "DELETE", path: "/v1/projects",
			body: `not json`, wantCode: "bad_request"},
		{name: "post-unknown-tool", method: "POST", path: "/v1/totally-not-a-tool",
			body: `{}`, wantCode: "not_found"},
		{name: "get-on-tool-route", method: "GET", path: "/v1/search",
			body: "", wantCode: "method_not_allowed"},
		{name: "tool-bad-args", method: "POST", path: "/v1/search",
			body: `{"query":""}`, wantCode: "tool_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			switch tc.method {
			case "GET":
				rec = httpGet(t, srv, tc.path)
			case "POST":
				rec = httpPost(t, srv, tc.path, tc.body)
			case "DELETE":
				rec = httpDelete(t, srv, tc.path, tc.body)
			default:
				t.Fatalf("unsupported method %q", tc.method)
			}
			if rec.Code < 400 || rec.Code >= 600 {
				t.Fatalf("status %d, want 4xx/5xx", rec.Code)
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("not JSON: %v\nbody: %s", err, rec.Body.String())
			}
			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("error not object; got %T (%v)", resp["error"], resp["error"])
			}
			if code, _ := errObj["code"].(string); code != tc.wantCode {
				t.Errorf("error.code = %q, want %q\nresp: %v", code, tc.wantCode, resp)
			}
			if msg, _ := errObj["message"].(string); msg == "" {
				t.Errorf("error.message empty\nresp: %v", resp)
			}
		})
	}
}

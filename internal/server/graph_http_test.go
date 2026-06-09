// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

func seedGraphHTTPProject(t *testing.T, store *db.Store) string {
	t.Helper()
	projectID := "/tmp/pincher-graph-http"
	if err := store.UpsertProjectMeta(db.Project{
		ID:        projectID,
		Path:      projectID,
		Name:      "graph-http",
		IndexedAt: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("UpsertProjectMeta: %v", err)
	}
	syms := []db.Symbol{
		{ID: projectID + "::alpha", ProjectID: projectID, FilePath: "alpha.go", Name: "Alpha", QualifiedName: "pkg.Alpha", Kind: "Function", Language: "Go", StartLine: 10, EndLine: 20, ExtractionConfidence: 1},
		{ID: projectID + "::beta", ProjectID: projectID, FilePath: "beta.go", Name: "Beta", QualifiedName: "pkg.Beta", Kind: "Method", Language: "Go", StartLine: 30, EndLine: 40, ExtractionConfidence: 1},
		{ID: projectID + "::gamma", ProjectID: projectID, FilePath: "gamma.py", Name: "Gamma", QualifiedName: "pkg.Gamma", Kind: "Class", Language: "Python", StartLine: 50, EndLine: 60, ExtractionConfidence: 0.9},
	}
	if err := store.BulkUpsertSymbols(syms); err != nil {
		t.Fatalf("BulkUpsertSymbols: %v", err)
	}
	if err := store.BulkUpsertEdges([]db.Edge{
		{ProjectID: projectID, FromID: syms[0].ID, ToID: syms[1].ID, Kind: "calls", Confidence: 1, Source: "resolve_pass"},
		{ProjectID: projectID, FromID: syms[1].ID, ToID: syms[2].ID, Kind: "imports", Confidence: 0.8, Source: "resolve_pass"},
	}); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}
	return projectID
}

func TestServeHTTP_GraphEndpoint_BoundedFilterableAndAdditive(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	projectID := seedGraphHTTPProject(t, store)

	req := httptest.NewRequest(http.MethodGet, "/v1/graph?project="+projectID+"&limit=2&filter=alpha", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/graph: got %d want 200 body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Project        string           `json:"project"`
		Limit          int              `json:"limit"`
		TotalSymbols   int              `json:"total_symbols"`
		TotalEdges     int              `json:"total_edges"`
		HasMoreSymbols bool             `json:"has_more_symbols"`
		Symbols        []map[string]any `json:"symbols"`
		Edges          []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal graph response: %v\n%s", err, w.Body.String())
	}
	if body.Project != projectID || body.Limit != 2 {
		t.Fatalf("project/limit mismatch: %#v", body)
	}
	if body.TotalSymbols != 3 || body.TotalEdges != 2 {
		t.Fatalf("totals should describe the full graph before filter/window, got symbols=%d edges=%d", body.TotalSymbols, body.TotalEdges)
	}
	if len(body.Symbols) != 1 || body.Symbols[0]["name"] != "Alpha" {
		t.Fatalf("filter should return only Alpha, got %#v", body.Symbols)
	}
	if len(body.Edges) != 0 {
		t.Fatalf("filtered graph should omit edges whose endpoints are outside the visible node set, got %#v", body.Edges)
	}
	for _, key := range []string{"id", "name", "qualified_name", "kind", "language", "file_path", "start_line", "end_line", "extraction_confidence"} {
		if _, ok := body.Symbols[0][key]; !ok {
			t.Fatalf("symbol missing legacy graph key %q: %#v", key, body.Symbols[0])
		}
	}
}

func TestServeHTTP_GraphEndpoint_FilterMatchesPathKindAndLanguage(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	projectID := seedGraphHTTPProject(t, store)

	for _, tc := range []struct {
		name string
		q    string
		want string
	}{
		{name: "file_path", q: "gamma.py", want: "Gamma"},
		{name: "kind", q: "method", want: "Beta"},
		{name: "language", q: "python", want: "Gamma"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/v1/graph?project="+projectID+"&filter="+tc.q, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("GET /v1/graph: got %d want 200 body=%s", w.Code, w.Body.String())
			}
			var body struct {
				Symbols []map[string]any `json:"symbols"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal graph response: %v", err)
			}
			if len(body.Symbols) != 1 || body.Symbols[0]["name"] != tc.want {
				t.Fatalf("filter %q should return %s, got %#v", tc.q, tc.want, body.Symbols)
			}
		})
	}
}

func TestServeHTTP_GraphEndpoint_LimitBoundsVisibleNodesAndEdges(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	projectID := seedGraphHTTPProject(t, store)

	req := httptest.NewRequest(http.MethodGet, "/v1/graph?project="+projectID+"&limit=2", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/graph: got %d want 200 body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Limit          int              `json:"limit"`
		HasMoreSymbols bool             `json:"has_more_symbols"`
		Symbols        []map[string]any `json:"symbols"`
		Edges          []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal graph response: %v", err)
	}
	if body.Limit != 2 || len(body.Symbols) != 2 || !body.HasMoreSymbols {
		t.Fatalf("limit window mismatch: %#v", body)
	}
	if len(body.Edges) != 1 {
		t.Fatalf("only edges between visible capped nodes should be returned, got %#v", body.Edges)
	}
	for _, key := range []string{"from", "to", "kind", "source", "confidence"} {
		if _, ok := body.Edges[0][key]; !ok {
			t.Fatalf("edge missing legacy graph key %q: %#v", key, body.Edges[0])
		}
	}
}

func TestServeHTTP_GraphEndpoint_DefaultsToSessionProjectAndCapsLimit(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	projectID := seedGraphHTTPProject(t, store)
	srv.sessionID = projectID

	req := httptest.NewRequest(http.MethodGet, "/v1/graph?limit=9999", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/graph: got %d want 200 body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal graph response: %v", err)
	}
	if body["project"] != projectID {
		t.Fatalf("project should default to session project, got %#v", body["project"])
	}
	if got := int(body["limit"].(float64)); got != 500 {
		t.Fatalf("limit should be capped at 500, got %d", got)
	}
}

func TestServeHTTP_GraphEndpoint_ProjectResolutionErrors(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)

	for _, tc := range []struct {
		name string
		url  string
		want int
	}{
		{name: "missing_project", url: "/v1/graph", want: http.StatusBadRequest},
		{name: "unknown_project", url: "/v1/graph?project=/tmp/does-not-exist", want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("GET %s: got %d want %d body=%s", tc.url, w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestServeHTTP_GraphEndpoint_GetOnly(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/graph", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/graph: got %d want 405 body=%s", w.Code, w.Body.String())
	}
	if allow := w.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow = %q, want GET, HEAD", allow)
	}
}

func TestDashboardHTML_IncludesGraphTab(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/dashboard", nil)
	srv.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{`data-args='["graph"]'>Graph`, `id="tab-graph"`, `id="graph-canvas"`, `id="graph-project"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard HTML missing %q", want)
		}
	}
}

func TestDashboardJS_IncludesGraphRendererAndEndpoint(t *testing.T) {
	t.Parallel()
	js := renderDashboardJS("")
	for _, want := range []string{"loadGraph", "renderGraph", "tabFetch('graph', '/v1/graph", "graph-canvas"} {
		if !strings.Contains(js, want) {
			t.Fatalf("dashboard JS missing %q", want)
		}
	}
}

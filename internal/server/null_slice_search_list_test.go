// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// #334: search and list endpoints must return [] (not null) for the
// empty-result branches. Final piece of the JSON-shape sweep started
// in #328 → #330 → #332.

// search with a query that returns no hits → "results":[] not null.
// Seeds an empty-but-indexed project so the auto-index path is skipped
// and we exercise the actual zero-results branch inside handleSearch.
func TestHandleSearch_ZeroResults_ResultsIsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	pid := t.TempDir() // real path so auto-index sees an existing root
	store.UpsertProject(db.Project{ID: pid, Path: pid, Name: "search-empty", IndexedAt: time.Now()})
	srv.sessionID = pid
	srv.sessionRoot = pid

	result, err := srv.handleSearch(context.Background(), makeReq(map[string]any{
		"query":   "definitely_does_not_exist_query_xyz_qqq",
		"project": pid,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	body := decode(t, result)
	if v, present := body["results"]; !present {
		t.Fatal("results key missing from search response")
	} else if v == nil {
		t.Errorf("results is null; want [] (non-nil empty array)")
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), `"results":null`) {
		t.Errorf("search JSON contains \"results\":null; want \"results\":[]\nfull: %s", raw)
	}
}

// list on a server with zero indexed projects → "projects":[] not null.
func TestHandleList_NoProjects_ProjectsIsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	// Don't seed any project — we want the empty-store branch.

	result, err := srv.handleList(context.Background(), makeReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	body := decode(t, result)
	if v, present := body["projects"]; !present {
		t.Fatal("projects key missing from list response")
	} else if v == nil {
		t.Errorf("projects is null; want [] (non-nil empty array)")
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), `"projects":null`) {
		t.Errorf("list JSON contains \"projects\":null; want \"projects\":[]\nfull: %s", raw)
	}
}

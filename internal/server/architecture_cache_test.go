// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

func TestArchitectureCacheHitAndInvalidation(t *testing.T) {
	srv := &Server{}
	p := &db.Project{
		ID:        "proj",
		IndexedAt: time.Unix(100, 0),
		SymCount:  10,
		EdgeCount: 20,
	}
	data := map[string]any{
		"languages": map[string]int{"Go": 10},
		"_meta":     map[string]any{"next_steps": []map[string]string{{"tool": "search"}}},
	}

	srv.setArchitectureCache("proj", false, p, data)
	got, ok := srv.getArchitectureCache("proj", false, p)
	if !ok {
		t.Fatal("expected cache hit")
	}
	got["_meta"].(map[string]any)["latency_ms"] = 1

	gotAgain, ok := srv.getArchitectureCache("proj", false, p)
	if !ok {
		t.Fatal("expected second cache hit")
	}
	if _, leaked := gotAgain["_meta"].(map[string]any)["latency_ms"]; leaked {
		t.Fatal("cache hit returned shared _meta map")
	}

	changed := *p
	changed.EdgeCount++
	if _, ok := srv.getArchitectureCache("proj", false, &changed); ok {
		t.Fatal("expected cache miss after project totals changed")
	}
}

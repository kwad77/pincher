// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// `mcp__pincher__query` cypher parse/syntax errors used to return a
// bare errResult — the agent saw `"cypher error: ..."` with no
// remediation, no link to schema/guide. Most errors (BETWEEN, !=,
// arithmetic) already get an inline hint via the operatorHint table,
// but malformed regex / unknown predicates / type mismatches all
// fell through to the bare envelope. Now wraps with errResultRich
// so every cypher failure carries schema + working-example + guide
// next_steps.

func TestHandleQuery_CypherError_RichEnvelopeWithNextSteps(t *testing.T) {
	t.Parallel()
	srv, store, _ := newTestServer(t)
	srv.sessionID = "pcqerr"
	srv.sessionRoot = "/tmp/pcqerr"
	store.UpsertProject(db.Project{
		ID: "pcqerr", Path: "/tmp/pcqerr", Name: "pcqerr", IndexedAt: time.Now(),
	})

	res, err := srv.handleQuery(context.Background(), makeReq(map[string]any{
		"pinchql": `MATCH (n:Function) WHERE n.name =~ "[invalid(" RETURN n.name LIMIT 1`,
		"project": "pcqerr",
	}))
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	body := decode(t, res)
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "cypher error") {
		t.Errorf("expected 'cypher error' in error; got %q", errStr)
	}
	meta, _ := body["_meta"].(map[string]any)
	steps, _ := meta["next_steps"].([]any)
	if len(steps) < 3 {
		t.Fatalf("expected at least 3 next_steps (schema + query example + guide); got %d (%v)", len(steps), steps)
	}
	wantTools := map[string]bool{"schema": false, "query": false, "guide": false}
	for _, s := range steps {
		step, _ := s.(map[string]any)
		if tool, _ := step["tool"].(string); wantTools[tool] == false {
			if _, present := wantTools[tool]; present {
				wantTools[tool] = true
			}
		}
	}
	for tool, found := range wantTools {
		if !found {
			t.Errorf("expected next_step for %q tool; got %v", tool, steps)
		}
	}
}

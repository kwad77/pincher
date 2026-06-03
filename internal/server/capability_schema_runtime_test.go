// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1202 v0.66: schema_vN capability tag is now a runtime probe
// against db.CurrentSchemaVersion() — not a compiled-in constant.
// Pre-fix the hardcoded "schema_v26" / "schema_v27" / etc. tag
// could go stale relative to the actual DB schema when migrations
// shipped without rebuilding/restarting the binary. Caught
// in v0.66 DOGFOOD round 1: running binary advertised schema_v26
// but the schema row read v27 because the v27 migration had been
// applied via a concurrent process. Silent lie to routers.
//
// Table-from-the-start (#1152):
//   - Positive: advertised tag matches CurrentSchemaVersion() exactly.
//   - Negative: no hardcoded "schema_v\d+" literal in capabilities
//     output other than the one currently in scope.
//   - Cross-check: when both `_meta.capabilities` and the response's
//     top-level `schema_version` are surfaced together (e.g. health),
//     they must agree.

func TestCapabilities_SchemaTagMatchesRuntime(t *testing.T) {
	srv, _, _ := newTestServer(t)
	want := fmt.Sprintf("schema_v%d", db.CurrentSchemaVersion())
	found := false
	for _, c := range srv.capabilities {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("capabilities missing %q (CurrentSchemaVersion()=%d)\nGOT: %v",
			want, db.CurrentSchemaVersion(), srv.capabilities)
	}
}

// Negative: no OTHER schema_vN tag should appear. Pre-fix a stale
// constant could persist alongside a new one if someone edited
// without removing the old. The runtime probe guarantees exactly
// one schema_vN tag.
func TestCapabilities_OnlyOneSchemaTag(t *testing.T) {
	srv, _, _ := newTestServer(t)
	count := 0
	for _, c := range srv.capabilities {
		if strings.HasPrefix(c, "schema_v") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_vN capability tag; got %d in %v",
			count, srv.capabilities)
	}
}

// Cross-check: the version surfaced by db.CurrentSchemaVersion()
// (which derives from len(schemaMigrations)+1) is the SAME number
// the migrate path actually uses to bump schema_version. Pin
// against drift between the runtime probe source and the actual
// migration head. Pre-1.0 this hasn't been a problem; the gate
// catches a future hypothetical refactor that splits the two.
func TestCurrentSchemaVersion_MatchesMigrationHead(t *testing.T) {
	_, store, _ := newTestServer(t)
	var dbVer int
	if err := store.RO().QueryRow(`SELECT version FROM schema_version`).Scan(&dbVer); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	got := db.CurrentSchemaVersion()
	if dbVer != got {
		t.Errorf("schema_version row=%d, CurrentSchemaVersion()=%d — they must agree (runtime probe drives the capability tag)",
			dbVer, got)
	}
}

// SPDX-License-Identifier: MIT

package index

import "testing"

// #1899: binary-drift/full-reindex writes must not monopolize the shared
// SQLite writer with larger-than-normal symbol batches. The live HTTP/MCP
// service persists session/tool-call counters through the same writer; large
// forced-reindex transactions caused SQLITE_BUSY deferrals while an operator
// CLI refresh was rebuilding a large project. Full reindex may skip per-file
// deletes, but its symbol flush batch must stay advisory-write friendly.
func TestFlushSymbolThreshold_FullReindexDoesNotUseLargeMaintenanceBatch(t *testing.T) {
	regular := flushSymbolThresholdFor(false)
	full := flushSymbolThresholdFor(true)

	if regular <= 0 {
		t.Fatalf("regular threshold = %d; want positive", regular)
	}
	if full != regular {
		t.Fatalf("full reindex threshold = %d; want %d so forced binary-drift reindex yields the writer as often as normal indexing (#1899)", full, regular)
	}
	if full > 1000 {
		t.Fatalf("full reindex threshold = %d; want <= 1000 to bound SQLite writer lock duration (#1899)", full)
	}
}

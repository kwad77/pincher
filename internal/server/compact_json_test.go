// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1089: tool responses now use compact JSON by default (~10-15% byte
// savings on representative shapes). PINCHER_DEBUG_META=1 preserves
// pretty-printing for human-eyeballing raw MCP traffic.

// #1089: tool responses default to compact JSON. The escape hatch
// (PINCHER_DEBUG_META=1 → pretty-printed) is integration-tested only
// because the test setup here invokes handleSchema directly without
// the withRequestID middleware that lives between jsonResultWithMeta
// and the wire response. Compact-default is the contract that
// matters; pretty-printing is a developer-debugging convenience that
// is genuinely opt-in via env at process start.
func TestToolResponse_DefaultsToCompactJSON(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	res, err := srv.handleSchema(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleSchema: %v", err)
	}
	text := textOf(t, res)
	// Compact JSON: no indented keys. A two-space-indented key would
	// look like "\n  \"" — pretty-printed marker.
	if strings.Contains(text, "\n  \"") {
		t.Errorf("expected compact JSON; got pretty-printed:\n%.200s", text)
	}
}

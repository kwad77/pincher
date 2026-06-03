// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"testing"
)

// #1018: handleInit hard-rejects target=continue (always-global,
// escapes project_path from MCP context). But when handleInit fell
// through to pinit.ResolveTargets's "unknown --target" error, that
// error's enumeration of valid targets STILL listed `continue` —
// the CLI's view, not MCP's. So the error told MCP callers:
//
//   "unknown --target 'foo' (one of: claude, cursor, ..., continue, ...)"
//
// A caller reading that, then trying `target=continue`, immediately
// hit the hard-reject. Contract drift inside one tool. Now: strip
// `continue` from the enumeration in MCP context so the error is
// truthful — every named target works.

func TestHandleInit_UnknownTargetError_DoesNotListContinue(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.sessionRoot = t.TempDir()

	res, err := srv.handleInit(context.Background(), makeReq(map[string]any{
		"target": "nonexistent-editor",
	}))
	if err != nil {
		t.Fatalf("handleInit: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}

	body := decode(t, res)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "unknown --target") {
		t.Fatalf("expected 'unknown --target' message; got %q", msg)
	}
	if !strings.Contains(msg, "one of:") {
		t.Fatalf("expected 'one of:' enumeration; got %q", msg)
	}
	// continue must not appear in the valid-targets list since MCP
	// hard-rejects it. Match the comma-bounded form to avoid catching
	// any future English phrasing that contains the word "continue".
	if strings.Contains(msg, ", continue,") || strings.Contains(msg, ", continue)") {
		t.Errorf("MCP error enumeration must NOT list `continue` as a valid target; got %q", msg)
	}
	// Sanity: a real valid target should still appear in the message.
	if !strings.Contains(msg, "claude") {
		t.Errorf("expected real targets like `claude` to still appear; got %q", msg)
	}
}

// Regression guard: the hard-reject path for target=continue still
// fires (we're stripping continue from the error message only, not
// the underlying enforcement).
func TestHandleInit_ContinueTarget_StillHardRejected(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTestServer(t)
	srv.sessionRoot = t.TempDir()

	res, err := srv.handleInit(context.Background(), makeReq(map[string]any{
		"target": "continue",
	}))
	if err != nil {
		t.Fatalf("handleInit: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError; got %s", textOf(t, res))
	}
	body := decode(t, res)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "not available via MCP") {
		t.Errorf("expected continue-specific hard-reject message; got %q", msg)
	}
}

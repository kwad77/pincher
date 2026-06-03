// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #278: health surfaces a binary_stale flag when a newer pincher
// binary is on disk than the one this MCP server is running.
//
// The detector runs against the captured binary path + start mtime;
// to test deterministically we plant a fake binary in a temp dir,
// override the captured fields on the *Server, then mutate the
// file's mtime forward.

func TestHandleHealth_BinaryStaleFlagSet(t *testing.T) {
	srv, _, _ := newTestServer(t)
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "pincher.exe")
	if err := os.WriteFile(fakeBin, []byte("#fake"), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	info, _ := os.Stat(fakeBin)
	srv.binaryPath = fakeBin
	srv.binaryStartMTime = info.ModTime()

	// Move mtime forward to simulate `go build` replacing the file.
	future := info.ModTime().Add(10 * time.Minute)
	if err := os.Chtimes(fakeBin, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	res, err := srv.handleHealth(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, res)
	if stale, _ := body["binary_stale"].(bool); !stale {
		t.Errorf("binary_stale = %v, want true after on-disk mtime moved forward", body["binary_stale"])
	}
	if msg, _ := body["binary_stale_message"].(string); msg == "" {
		t.Error("expected binary_stale_message to be present")
	}
}

// Auto-restart-on-drift changes the user-facing recovery flow: with the
// env var set the supervisor handles the swap, so the message must not
// tell the caller to run /mcp reconnect manually. Without the env var
// set the caller is on their own and the explicit reconnect hint is
// load-bearing.
func TestHandleHealth_BinaryStaleMessageVariesByAutoRestartEnv(t *testing.T) {
	plant := func(t *testing.T) (*Server, string) {
		t.Helper()
		srv, _, _ := newTestServer(t)
		// Auto-restart path fires when env var=1 and binary is replaced;
		// stub exitFn so the test process isn't killed.
		srv.exitFn = func(int) {}
		tmp := t.TempDir()
		fakeBin := filepath.Join(tmp, "pincher.exe")
		if err := os.WriteFile(fakeBin, []byte("#fake"), 0o755); err != nil {
			t.Fatalf("write fake bin: %v", err)
		}
		info, _ := os.Stat(fakeBin)
		srv.binaryPath = fakeBin
		srv.binaryStartMTime = info.ModTime()
		future := info.ModTime().Add(10 * time.Minute)
		if err := os.Chtimes(fakeBin, future, future); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		return srv, fakeBin
	}

	t.Run("auto_restart_off_recommends_mcp_reconnect", func(t *testing.T) {
		t.Setenv(autoRestartEnvVar, "")
		srv, _ := plant(t)
		res, err := srv.handleHealth(context.Background(), makeReq(nil))
		if err != nil {
			t.Fatalf("handleHealth: %v", err)
		}
		msg, _ := decode(t, res)["binary_stale_message"].(string)
		if msg == "" {
			t.Fatal("expected binary_stale_message")
		}
		if !strings.Contains(msg, "/mcp reconnect") {
			t.Errorf("auto-restart off: message should recommend /mcp reconnect; got %q", msg)
		}
	})

	t.Run("auto_restart_on_announces_supervisor_respawn", func(t *testing.T) {
		t.Setenv(autoRestartEnvVar, "1")
		srv, _ := plant(t)
		res, err := srv.handleHealth(context.Background(), makeReq(nil))
		if err != nil {
			t.Fatalf("handleHealth: %v", err)
		}
		msg, _ := decode(t, res)["binary_stale_message"].(string)
		if msg == "" {
			t.Fatal("expected binary_stale_message")
		}
		if strings.Contains(msg, "/mcp reconnect") {
			t.Errorf("auto-restart on: message must not recommend /mcp reconnect (supervisor handles it); got %q", msg)
		}
		if !strings.Contains(msg, "supervisor") {
			t.Errorf("auto-restart on: message should announce supervisor respawn; got %q", msg)
		}
	})
}

func TestHandleHealth_BinaryNotStale(t *testing.T) {
	srv, _, _ := newTestServer(t)
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "pincher.exe")
	if err := os.WriteFile(fakeBin, []byte("#fake"), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	info, _ := os.Stat(fakeBin)
	srv.binaryPath = fakeBin
	srv.binaryStartMTime = info.ModTime()

	// Don't touch the file — mtime stays at start time.
	res, err := srv.handleHealth(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, res)
	if _, present := body["binary_stale"]; present {
		t.Errorf("binary_stale should be absent when binary hasn't been replaced; body=%v", body)
	}
}

func TestHandleHealth_BinaryStaleFieldsMissingWhenUnknown(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// Simulate startup capture failure.
	srv.binaryPath = ""
	srv.binaryStartMTime = time.Time{}

	res, err := srv.handleHealth(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	body := decode(t, res)
	if _, present := body["binary_stale"]; present {
		t.Errorf("binary_stale should be absent when capture failed; body=%v", body)
	}
}

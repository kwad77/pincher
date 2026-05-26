package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// healthcheck.go shipped in v0.11.0 with 0% test coverage — covered only
// by manual smoke tests. These tests close that gap.

func TestReplyToServerRequest_RootsList_ReturnsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	id := json.RawMessage(`42`)
	replyToServerRequest(&buf, id, "roots/list", false)

	var got struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  struct {
			Roots []any `json:"roots"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, buf.String())
	}
	if got.JSONRPC != "2.0" {
		t.Errorf("jsonrpc=%q want %q", got.JSONRPC, "2.0")
	}
	if string(got.ID) != "42" {
		t.Errorf("id=%s want 42", string(got.ID))
	}
	if got.Result.Roots == nil || len(got.Result.Roots) != 0 {
		t.Errorf("roots=%v want empty array (must not be null — server iterates without null-check)", got.Result.Roots)
	}
}

func TestReplyToServerRequest_UnknownMethod_ReturnsError(t *testing.T) {
	var buf bytes.Buffer
	id := json.RawMessage(`"abc"`)
	replyToServerRequest(&buf, id, "sampling/createMessage", false)

	var got struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error.Code != -32601 {
		t.Errorf("error.code=%d want -32601 (method not found)", got.Error.Code)
	}
	if !strings.Contains(got.Error.Message, "sampling/createMessage") {
		t.Errorf("error.message=%q want to mention the rejected method", got.Error.Message)
	}
}

// healthCheckProbe end-to-end: spawn a real pincher built via the
// buildPincherBinary helper, complete the MCP handshake, exit 0.
// Covers the happy path for the bulk of healthcheck.go.
func TestHealthCheckProbe_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipping in -short")
	}
	bin := buildPincherBinary(t)
	t.Setenv("PINCHER_DATA_DIR", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := healthCheckProbe(ctx, bin, nil, false); err != nil {
		t.Fatalf("healthCheckProbe: %v", err)
	}
}

// Probe failure: non-existent binary path. Exercises the spawn-error
// branch.
func TestHealthCheckProbe_MissingBinary_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := healthCheckProbe(ctx, "/path/that/definitely/does/not/exist/pincher", nil, false)
	if err == nil {
		t.Fatal("expected error for missing binary; got nil")
	}
	if !strings.Contains(err.Error(), "spawn") && !strings.Contains(err.Error(), "stdin") && !strings.Contains(err.Error(), "stdout") {
		t.Errorf("error %q should reference spawn/stdin/stdout — gives operators a hint", err.Error())
	}
}

// Probe deadline: cancelled context terminates the probe cleanly.
func TestHealthCheckProbe_ContextCancelled_ReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipping in -short")
	}
	bin := buildPincherBinary(t)
	t.Setenv("PINCHER_DATA_DIR", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // ensure deadline elapsed before spawn

	if err := healthCheckProbe(ctx, bin, nil, false); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// runHealthCheckCLI integration: spawn the just-built pincher with
// `health-check` and assert exit 0. Exercises the CLI dispatch wrapper
// + flag parsing + success path end-to-end. Coverage via the -cover
// instrumented binary (see coverbuild_test.go's helper).
func TestRunHealthCheckCLI_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	bin := buildPincherBinary(t)
	t.Setenv("PINCHER_DATA_DIR", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "health-check", "--timeout", "10s")
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pincher health-check: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "OK pincher") {
		t.Errorf("expected 'OK pincher' in output; got %q", string(out))
	}
}

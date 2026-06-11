// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kwad77/pincher/internal/supervisor"
)

const supervisedInnerBinaryEnv = "PINCHER_SUPERVISED_INNER_BINARY"

// ensureSessionIDEnv returns env with a PINCHER_SESSION_ID set if the
// caller hadn't provided one. The supervisor stamps this once per
// supervisor lifetime so all inner respawns share one sessions row
// (#420). Pure function; the helper exists so the env-building logic
// is unit-testable without spinning up the full supervisor.
func ensureSessionIDEnv(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "PINCHER_SESSION_ID=") {
			return env
		}
	}
	return append(env, fmt.Sprintf("PINCHER_SESSION_ID=sup-%d", time.Now().UnixNano()))
}

// runSupervisedCLI is the `pincher supervised` entry point. It runs a
// long-lived process that wraps an inner pincher MCP server with
// auto-respawn + initialize-replay, so the MCP client (Claude Code,
// Codex, etc.) sees an unbroken stdio session even when the inner
// exits — whether from PINCHER_AUTO_RESTART_ON_DRIFT firing on a binary
// upgrade, an unrecoverable panic, or an OS-level kill.
//
// Configure your MCP client to invoke `pincher supervised` instead of
// `pincher`, and the manual `/mcp` reconnect dance disappears.
//
// Note: passes through any args after `supervised` to the inner pincher
// (`pincher supervised --slow-query-ms 100` runs the inner with that
// flag). The supervisor-only `--inner-binary PATH` flag is stripped before
// forwarding, so a stable provider can supervise a dirty action binary.
func runSupervisedCLI(args []string) {
	if supervisedArgsWantHelp(args) {
		printSupervisedUsage(os.Stdout)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher supervised: cannot resolve own binary: %v\n", err)
		os.Exit(1)
	}

	innerPath, innerArgs, err := supervisedInnerBinary(args, exe, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pincher supervised: %v\n", err)
		os.Exit(1)
	}

	sup := supervisor.New(innerPath)
	sup.ProviderPath = exe
	sup.ProviderVersion = version
	sup.ActionVersionFunc = func(path string) string {
		return detectPincherBinaryVersion(path, exe, version)
	}
	sup.InnerArgs = innerArgs
	// #420: stamp a stable PINCHER_SESSION_ID so successive inner
	// processes share one sessions-table row. The inner reads this on
	// startup and seeds atomic counters from the prior flush, so the
	// SESSION stats survive supervised respawn instead of resetting
	// to zero on every binary swap. Inherits an existing value when
	// the user already set one (test harnesses, deliberate
	// multi-supervisor sharing).
	sup.Env = ensureSessionIDEnv(os.Environ())
	// #1901: when respawn gives up (binary-swap window outlasting the
	// retry budget, or the circuit breaker tripping on a flapping
	// inner), the default is now to degrade — keep the host transport
	// open, answer requests with JSON-RPC errors, and retry in the
	// background — because most MCP hosts never reopen a closed
	// transport without a full session restart. Operators whose host
	// DOES respawn MCP servers can opt back into exit-on-give-up.
	sup.ExitOnGiveUp = os.Getenv("PINCHER_SUPERVISED_EXIT_ON_GIVEUP") == "1"
	sup.Stdin = os.Stdin
	sup.Stdout = os.Stdout
	sup.Stderr = os.Stderr

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// #724: the supervisor detects a graceful client disconnect via
	// stdin EOF, but a client killed abnormally may not close the pipe.
	// Reap the supervisor (and, via Run's shutdownInner, its child) when
	// the MCP client is gone — otherwise the whole supervised tree
	// orphans and keeps Watch()ing the shared DB.
	watchParent(ctx, cancel)

	if err := sup.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "pincher supervised: %v\n", err)
		os.Exit(1)
	}
}

func supervisedArgsWantHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help")
}

func printSupervisedUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: pincher supervised [--inner-binary PATH] [pincher server flags...]")
	fmt.Fprintln(out, "  Run the MCP stdio server under an auto-restarting supervisor.")
	fmt.Fprintln(out, "  The supervisor replays initialize after inner restarts, so agent hosts")
	fmt.Fprintln(out, "  keep the same stdio session across binary swaps or crashes.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "  --inner-binary PATH   Supervise this pincher binary instead of the provider")
	fmt.Fprintln(out, "                        path. Also configurable with PINCHER_SUPERVISED_INNER_BINARY.")
	fmt.Fprintln(out, "  Remaining args are forwarded to the inner pincher server.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "  PINCHER_SUPERVISED_EXIT_ON_GIVEUP=1  Exit (closing the host transport) when")
	fmt.Fprintln(out, "                        respawn gives up, instead of degrading and retrying in")
	fmt.Fprintln(out, "                        the background (the default since #1901).")
}

func supervisedInnerBinary(args []string, defaultPath string, getenv func(string) string) (string, []string, error) {
	innerPath := strings.TrimSpace(getenv(supervisedInnerBinaryEnv))
	if innerPath == "" {
		innerPath = defaultPath
	}

	innerArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--inner-binary":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--inner-binary requires a path")
			}
			innerPath = strings.TrimSpace(args[i+1])
			if innerPath == "" {
				return "", nil, fmt.Errorf("--inner-binary requires a non-empty path")
			}
			i++
		case strings.HasPrefix(arg, "--inner-binary="):
			innerPath = strings.TrimSpace(strings.TrimPrefix(arg, "--inner-binary="))
			if innerPath == "" {
				return "", nil, fmt.Errorf("--inner-binary requires a non-empty path")
			}
		default:
			innerArgs = append(innerArgs, arg)
		}
	}
	return innerPath, innerArgs, nil
}

func detectPincherBinaryVersion(binaryPath, providerPath, providerVersion string) string {
	if sameBinaryPath(binaryPath, providerPath) {
		return providerVersion
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binaryPath, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return normalizePincherVersionOutput(string(out))
}

func sameBinaryPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil {
		a = absA
	}
	if errB == nil {
		b = absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func normalizePincherVersionOutput(out string) string {
	out = strings.TrimSpace(out)
	return strings.TrimPrefix(out, "pincherMCP v")
}

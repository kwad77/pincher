// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
)

// orient.go — friction fix (#1710 v0.92): a human who runs bare
// `pincher` in a terminal to see what it does gets a silent hang —
// pincher falls through to the MCP stdio loop and blocks on stdin
// forever. A real MCP client (Claude Code, Cursor, the supervisor)
// always pipes JSON-RPC over a stdin PIPE; an interactive TTY on stdin
// with no subcommand can only be a human exploring. Detect that and
// print an orientation instead of blocking. Mirrors `pincher setup`'s
// TTY-aware behavior.

// shouldOrientInteractive reports whether a bare `pincher` invocation
// is a human at a terminal rather than an MCP client. It is the pure
// predicate behind the main() gate so it can be unit-tested; main
// supplies stdinIsTTY from term.IsTerminal.
//
//   - httpAddr != ""  → the user asked for the HTTP server; run it.
//   - noStdio         → an intentionally-detached HTTP-only child.
//   - stdin is a pipe → a real MCP client is feeding JSON-RPC.
//
// Only when none of those hold — no HTTP, stdio expected, and stdin is
// an interactive terminal — is this a human who should be oriented.
func shouldOrientInteractive(httpAddr string, noStdio, stdinIsTTY bool) bool {
	return httpAddr == "" && !noStdio && stdinIsTTY
}

// printInteractiveOrientation tells a human what pincher is and which
// command they probably want.
func printInteractiveOrientation(out io.Writer) {
	fmt.Fprintln(out, "pincher is a code-intelligence MCP server — your editor or agent")
	fmt.Fprintln(out, "launches it; it is not meant to be run directly in a terminal.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Get started:")
	fmt.Fprintln(out, "  pincher setup     Wire pincher into your editors & agents (interactive)")
	fmt.Fprintln(out, "  pincher index     Index the project in the current directory")
	fmt.Fprintln(out, "  pincher web       Open the dashboard in a browser")
	fmt.Fprintln(out, "  pincher --help    Every command and flag")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "To run the MCP server here anyway, pipe a client to it or use")
	fmt.Fprintln(out, "`pincher supervised` (the entry point for agent CLIs).")
}

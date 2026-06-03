// SPDX-License-Identifier: MIT

package main

import (
	"os/exec"
	"runtime"
)

// browser.go — friction fix (#1710 v0.92): `pincher web` resolves the
// dashboard URL and prints it, leaving the user to copy-paste it into
// a browser. When `web` is run interactively it should just open the
// dashboard. Same friction-reduction shape as `pincher setup` and the
// bare-invocation orientation.

// browserOpenCommand returns the platform command that opens url in the
// user's default browser. Split out pure so the per-OS mapping is
// unit-testable without launching anything.
func browserOpenCommand(goos, url string) (name string, args []string) {
	switch goos {
	case "windows":
		// rundll32 is preferred over `cmd /c start` — start is a shell
		// builtin with quoting quirks; FileProtocolHandler is a direct
		// shell-open of the URL.
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

// openBrowser launches the default browser at url. It Start()s (not
// Run()s) the helper so `pincher web` never blocks on the browser
// process. A non-nil error means the launch could not even be
// attempted — callers treat that as best-effort and fall back to the
// already-printed URL.
func openBrowser(url string) error {
	name, args := browserOpenCommand(runtime.GOOS, url)
	return exec.Command(name, args...).Start()
}

// shouldOpenBrowser reports whether `pincher web` should auto-open the
// dashboard: only for an interactive human (stdout is a TTY), never in
// --json mode (scripted) and never when --no-open was passed.
func shouldOpenBrowser(stdoutTTY, jsonOut, noOpen bool) bool {
	return stdoutTTY && !jsonOut && !noOpen
}

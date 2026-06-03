// SPDX-License-Identifier: MIT

package init

import (
	"os"
	"strings"
)

// host.go — host-aware target resolution for `pincher init` (#1862).
//
// The bug this fixes: `pincher init` with no explicit target used to
// default to claude, so a Codex user got a CLAUDE.md + .claude/ hook
// they never asked for. Resolution is now: the agent pincher is
// actually running under (env signal) → editor marker files → refuse
// rather than guess. Shared by the CLI (`runInitCLI`) and the MCP
// `init` tool so both surfaces behave identically.

// hostEnvSignals maps an environment variable a host sets to the init
// target for that host. Table-shaped so confirming a new host's signal
// is a one-line add. Only signals reliable enough to key on belong
// here — a var that also leaks into unrelated shells (e.g. CODEX_HOME,
// an optional config-dir override) does not qualify; those hosts fall
// through to marker-file detection.
var hostEnvSignals = []struct{ Env, Target, Reason string }{
	{"CLAUDECODE", "claude", "CLAUDECODE is set"},
}

// DetectHostFromEnv identifies the agent host pincher is running inside
// from environment variables the host sets. Returns the init target
// name + a human reason, or ("","") when no host can be identified.
func DetectHostFromEnv() (target, reason string) {
	for _, h := range hostEnvSignals {
		if os.Getenv(h.Env) != "" {
			return h.Target, h.Reason
		}
	}
	return "", ""
}

// AutoResolveResult is the outcome of AutoResolveInitTarget.
type AutoResolveResult struct {
	Target  string // resolved target name — valid only when Decided is true
	Reason  string // human explanation of how the target was chosen
	Decided bool   // false → no host could be determined; the caller must refuse, never guess
}

// AutoResolveInitTarget picks the init target when `pincher init` (CLI
// or MCP tool) was invoked with no explicit target. Host-aware: it
// prefers the agent pincher is running under (env signal), falls back
// to editor marker files, and reports Decided=false rather than
// guessing when neither is conclusive — silently defaulting to claude
// is the #1862 bug.
func AutoResolveInitTarget(cwd string) AutoResolveResult {
	envTarget, envReason := DetectHostFromEnv()
	return resolveInitTarget(envTarget, envReason, DetectTargetsRaw(cwd))
}

// resolveInitTarget is the pure decision behind AutoResolveInitTarget,
// split out so every branch is unit-testable without depending on the
// machine's real environment or installed editors.
func resolveInitTarget(envTarget, envReason string, hits []Target) AutoResolveResult {
	if envTarget != "" {
		return AutoResolveResult{Target: envTarget, Reason: "running under host " + envTarget + " (" + envReason + ")", Decided: true}
	}
	switch len(hits) {
	case 0:
		return AutoResolveResult{Decided: false}
	case 1:
		return AutoResolveResult{
			Target:  hits[0].Name,
			Reason:  hits[0].Name + " marker file present",
			Decided: true,
		}
	default:
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i] = h.Name
		}
		return AutoResolveResult{
			Target:  "detect",
			Reason:  "multiple hosts detected (" + strings.Join(names, ", ") + ") — configuring all",
			Decided: true,
		}
	}
}

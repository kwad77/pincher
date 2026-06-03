// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// driftAction is the advisory verdict driftFor returns. driftFor itself
// only ever produces driftNone or driftActionWarn — the warn vs refuse
// split lives at the call site (writers escalate to refusal via
// checkDriftForWrite, readers attach the message via attachDriftWarning).
type driftAction int

const (
	driftNone driftAction = iota
	driftActionWarn
)

// normalizeVersion strips developer-build noise so two stamps with the
// same semantic release compare equal. Returns "" for unparseable or
// dev-only versions, signaling "skip the comparison."
//
// Inputs we expect to handle:
//   - "0.10.0" / "v0.10.0"           → "v0.10.0" (canonical)
//   - "0.10.0-dirty"                 → "v0.10.0" (release, dirty tree)
//   - "0.10.0-3-gabcdef"             → "v0.10.0" (3 commits ahead)
//   - "0.10.0-3-gabcdef-dirty"       → "v0.10.0"
//   - "dev" / "" / unparseable       → ""        (skip)
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "dev" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	// Strip git-describe suffixes ("v0.10.0-3-gabcdef" / "-dirty"). We
	// intentionally drop the pre-release/build suffix rather than
	// preserve it — semver's pre-release ordering would mark
	// "v0.10.0-dirty" as STRICTLY LESS than "v0.10.0", which would
	// flag every developer build as drift against any released
	// project. Coercing to the base release is the right semantic
	// for "are these binaries on the same logical line."
	if i := strings.IndexByte(v, '-'); i > 0 {
		v = v[:i]
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

// checkDriftForWrite returns a non-nil error when a write-class tool
// should refuse because the project was indexed by a NEWER binary.
// Returns nil when no drift, when versions can't be compared (dev
// build on either side), or when the project doesn't exist (caller's
// mustProject would have errored first).
//
// driftFor's returned action is advisory (driftActionWarn). Writers
// always escalate any non-zero drift to a refusal — the warn vs
// refuse split lives at the call site, not in the helper.
func (s *Server) checkDriftForWrite(projectID string) error {
	msg, action := s.driftFor(projectID)
	if action == driftNone {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

// driftFor returns the drift message and the action a caller should
// take. Callers in writer tools should treat driftActionRefuse as a
// hard stop; readers should treat driftActionWarn as a hint to attach
// the message to `_meta.binary_version_warning`.
//
// The bias is conservative: when normalization or semver comparison
// fails on either side, we return driftNone rather than warn or
// refuse. False positives (refusing a legitimate write) are worse
// than false negatives (missing a drift signal) because we already
// have the index_drift surface in `health` as a backstop.
func (s *Server) driftFor(projectID string) (string, driftAction) {
	if projectID == "" {
		return "", driftNone
	}
	p, err := s.store.GetProject(projectID)
	if err != nil || p == nil {
		return "", driftNone
	}
	self := normalizeVersion(s.version)
	proj := normalizeVersion(p.BinaryVersion)
	if self == "" || proj == "" {
		// At least one side is dev/unstamped — comparing is meaningless.
		return "", driftNone
	}
	if semver.Compare(self, proj) >= 0 {
		// Self is at least as new as the indexer — the existing
		// `index_drift` warning in `health` already covers the
		// converse direction (newer-self-on-older-project).
		return "", driftNone
	}
	msg := fmt.Sprintf(
		"project %q was indexed by pincher %s; current binary is %s. Reads continue with this warning attached; writes are blocked. Upgrade pincher to %s or newer to clear this. (Backstop: rerunning `index` with the newer binary will re-stamp the project.)",
		p.Name, p.BinaryVersion, s.version, p.BinaryVersion,
	)
	return msg, driftActionWarn
}

// isIndexStale reports whether the project was indexed by an OLDER
// binary than the running server (the index_drift direction surfaced
// in handleHealth). #1603 v0.84: used by empty-result branches to
// stamp `EmptyReasonStaleIndex` ahead of `EmptyReasonNoResultsInCorpus`
// when the verifier can't rescue the query — because an empty result
// against a stale index might be a false negative the user can fix by
// re-indexing, while NoResultsInCorpus implies "genuinely missing"
// (which the user can't fix by re-indexing).
//
// Returns (false, "") when projectID resolves to no project, when
// versions are unstamped/dev, or when self <= project (i.e.,
// driftFor's bidirectional drift but in the opposite direction).
//
// Note: this is the COMPLEMENT direction of driftFor — driftFor warns
// when self < project (you're behind the indexer); isIndexStale fires
// when self > project (you're ahead and the index needs refreshing
// against current rules). Both are forms of drift; the empty-result
// stamping cares about the self>project direction.
func (s *Server) isIndexStale(projectID string) (bool, string) {
	if projectID == "" {
		return false, ""
	}
	p, err := s.store.GetProject(projectID)
	if err != nil || p == nil {
		return false, ""
	}
	self := normalizeVersion(s.version)
	proj := normalizeVersion(p.BinaryVersion)
	if self == "" || proj == "" {
		// Dev / unstamped — comparison is meaningless either way.
		return false, ""
	}
	if semver.Compare(self, proj) <= 0 {
		// Self is older or equal; not stale in this direction.
		// driftFor handles the other direction.
		return false, ""
	}
	return true, fmt.Sprintf(
		"project %q was indexed by pincher %s; running server is %s. CALLS/resolution edges may reflect older extraction rules — empty results here might be false negatives. Re-index to refresh.",
		p.Name, p.BinaryVersion, s.version,
	)
}

// attachDriftWarning is a convenience for read-class handlers: if the
// project is drifted (older self on newer-indexed project), populate
// data["_meta"]["binary_version_warning"] with the diagnostic. No-op
// otherwise. Designed to be called immediately after mustProject in
// each reader.
//
// #620: emitted exactly once per (project, indexed-binary-version)
// pair per server process. Once the agent has seen the warning, every
// subsequent response in the same session carrying the same warning
// is noise — the underlying drift state hasn't changed. Repeating it
// trains agents to filter out `_meta` entirely, which kills the
// genuinely-useful warnings (#473, #499, #612). A fresh server process
// or a version change re-arms emission via the keyed cache.
//
// `data` is the response map the handler is building; it must be
// non-nil. The function takes care of allocating the `_meta` sub-map
// if absent.
func (s *Server) attachDriftWarning(data map[string]any, projectID string) {
	msg, action := s.driftFor(projectID)
	if action != driftActionWarn {
		return
	}
	// Per-session dedupe. Key includes the indexer's binary version so
	// that an upgrade (which the index command will re-stamp) re-arms
	// the warning rather than silently suppressing it.
	p, err := s.store.GetProject(projectID)
	if err != nil || p == nil {
		return
	}
	key := projectID + ":" + p.BinaryVersion
	if _, alreadyEmitted := s.driftWarningsEmitted.LoadOrStore(key, struct{}{}); alreadyEmitted {
		return
	}
	meta, _ := data["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		data["_meta"] = meta
	}
	meta["binary_version_warning"] = msg
}

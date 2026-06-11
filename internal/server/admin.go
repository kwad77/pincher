// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kwad77/pincher/internal/db"
	"github.com/kwad77/pincher/internal/index"
)

// admin.go hosts the three operations that previously had only a CLI
// surface (`pincher doctor`, `rebuild-fts`, `self-test`). Promoting them
// to MCP tools (#558 phase 2) means they're also reachable over HTTP
// via the `/v1/<tool>` dispatcher built in #560 — dashboards and ops
// automations can poll them without shelling out.
//
// Implementation note: the CLI commands live in package main, so we
// can't share code by import. The duplication is bounded (~80 LOC per
// op) and the CLI implementations are tested separately. The ground
// truth is the Store APIs both surfaces call (`ListProjects`,
// `ListExtractionFailures`, `ListSlowQueries`, `RebuildFTS`).

// doctorProjectSummary is one row of the doctor report's projects list.
// Package-level (not function-local) so largeDBAdvisory can take a slice
// of them.
type doctorProjectSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Files   int    `json:"files"`
	Symbols int    `json:"symbols"`
	Edges   int    `json:"edges"`
	// DBBytesEstimate is a best-effort per-project on-disk byte
	// estimate. Sum across projects undershoots db_size_bytes by
	// 10-40% (the gap is page slack + WAL + schema overhead that
	// can't be cheaply attributed per project). Load-bearing is
	// relative ordering, not absolute bytes — agents use this to
	// answer "which project should I delete first?" when the DB
	// crosses GB. #1220.
	DBBytesEstimate      int64  `json:"db_bytes_estimate"`
	IndexedAt            string `json:"indexed_at"`
	SchemaVersionAtIndex *int   `json:"schema_version_at_index,omitempty"`
	BinaryVersion        string `json:"binary_version,omitempty"`
	Stale                bool   `json:"stale"`
	StaleReason          string `json:"stale_reason,omitempty"`
	IndexState           string `json:"index_state,omitempty"`
	IndexStartedAt       string `json:"index_started_at,omitempty"`
}

const staleIndexRunningThreshold = 30 * time.Minute

func projectIndexStateFields(state string, startedAt int64) (string, string) {
	if state == "" || state == "complete" {
		return "", ""
	}
	started := ""
	if startedAt > 0 {
		started = time.Unix(startedAt, 0).UTC().Format(time.RFC3339)
	}
	return state, started
}

// projectSchemaStaleness mirrors cmd/pinch/project.go's staleness helper.
// The server package cannot import package main, so this is deliberately
// duplicated for MCP/HTTP doctor parity with the CLI report.
func projectSchemaStaleness(at *int, current int) (bool, string) {
	if at == nil {
		return true, "predates schema_version_at_index column (pre-v15)"
	}
	if *at < current {
		return true, fmt.Sprintf("indexed at v%d, current is v%d", *at, current)
	}
	return false, ""
}

func staleIndexRunningAdvisory(projects []doctorProjectSummary, now time.Time) string {
	type staleProject struct {
		project doctorProjectSummary
		age     time.Duration
	}
	var stale []staleProject
	for _, p := range projects {
		if p.IndexState != "running" || p.IndexStartedAt == "" {
			continue
		}
		started, err := time.Parse(time.RFC3339, p.IndexStartedAt)
		if err != nil {
			continue
		}
		age := now.Sub(started)
		if age < staleIndexRunningThreshold {
			continue
		}
		stale = append(stale, staleProject{project: p, age: age.Truncate(time.Minute)})
	}
	if len(stale) == 0 {
		return ""
	}
	if len(stale) > 3 {
		stale = stale[:3]
	}
	parts := make([]string, 0, len(stale))
	for _, s := range stale {
		label := s.project.Name
		if label == "" {
			label = s.project.ID
		}
		parts = append(parts, fmt.Sprintf("%q marked running for %s", label, s.age))
	}
	return fmt.Sprintf("Index state looks interrupted: %s. Rerun `pincher index --force <path>` (or POST /v1/index with force=true) for the affected project to refresh data and clear the marker. See #1905.", strings.Join(parts, "; "))
}

func projectSchemaDriftAdvisory(projects []doctorProjectSummary, currentSchema int) string {
	if currentSchema <= 0 {
		return ""
	}
	type driftedProject struct {
		project doctorProjectSummary
		reason  string
	}
	var drifted []driftedProject
	for _, p := range projects {
		switch {
		case p.SchemaVersionAtIndex == nil:
			drifted = append(drifted, driftedProject{project: p, reason: "pre-v15"})
		case *p.SchemaVersionAtIndex < currentSchema:
			drifted = append(drifted, driftedProject{
				project: p,
				reason:  fmt.Sprintf("v%d < v%d", *p.SchemaVersionAtIndex, currentSchema),
			})
		}
	}
	if len(drifted) == 0 {
		return ""
	}
	labelCounts := map[string]int{}
	for _, d := range drifted {
		label := d.project.Name
		if label == "" {
			label = d.project.ID
		}
		labelCounts[label]++
	}
	extra := 0
	if len(drifted) > 3 {
		extra = len(drifted) - 3
		drifted = drifted[:3]
	}
	parts := make([]string, 0, len(drifted))
	for _, d := range drifted {
		label := d.project.Name
		if label == "" {
			label = d.project.ID
		}
		if labelCounts[label] > 1 && d.project.Path != "" {
			label += " at " + d.project.Path
		}
		parts = append(parts, fmt.Sprintf("%q (%s)", label, d.reason))
	}
	if extra > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", extra))
	}
	return "Projects indexed by an older schema: " + strings.Join(parts, "; ") +
		". Search/trace/context may reflect stale extractor or resolver behavior until they are refreshed. " +
		"Remediation: rerun `pincher index --force <path>` (or POST /v1/index with force=true) for each affected project."
}

// ghostProjectAdvisory returns a human-readable health advisory when a
// project has substantial symbols but zero (or vanishingly few) edges —
// the "ghost-edges" signature of #815 / #836 (zelosMCP user report).
// Extraction half-succeeded: symbols got persisted but the resolver
// phase never ran (or its writes got lost). At scale, the broken
// project still answers `search` happily, then `trace`/`query` over
// the symbol returns zero rows that look like a real empty result.
// Pre-#1009, doctor reported the totals as bare numbers — a 368k-symbol
// project with 0 edges looked identical to a healthy 368k/N project in
// the listing.
//
// Two thresholds, both anchored in dogfood-observed numbers:
//
//   - Strict (Symbols >= 1000 && Edges == 0): the original #1009 gate.
//     Below 1000 symbols a true pure-config / pure-docs repo can
//     legitimately land at 0 edges. At 1000+ symbols the project
//     almost certainly contains code files, and code without edges
//     means the resolver lost its work.
//
//   - Ratio (Symbols >= 1000 && Edges/Symbols < 0.001): the #1010
//     extension. Some ghost projects leak a handful of edges but the
//     resolver-failure shape is the same. Empirical: warp_rc indexed
//     at 1.4M symbols / 247 edges (ratio 0.000175) — clearly ghost
//     but the strict gate missed it. Healthy projects on the same DB
//     sit at >= 0.01 edges/symbol (Codex hits 0.040 in worst case);
//     the 0.001 floor is two orders of magnitude below the lowest
//     healthy ratio.
func ghostProjectAdvisory(projects []doctorProjectSummary) string {
	const symThreshold = 1000
	// #1010: ratio floor for the "vanishingly few edges" arm. Picked
	// to sit two orders of magnitude below the worst-case healthy
	// project's ratio (~0.04 on the dogfood box). Tightening below
	// 0.001 risks tripping on legitimate config-heavy repos with a
	// small Go cmd directory; widening above risks missing the
	// ratio-class ghosts the strict gate already misses.
	const minHealthyRatio = 0.001
	var ghosts []doctorProjectSummary
	for _, p := range projects {
		if p.Symbols < symThreshold {
			continue
		}
		if p.Edges == 0 {
			ghosts = append(ghosts, p)
			continue
		}
		if float64(p.Edges)/float64(p.Symbols) < minHealthyRatio {
			ghosts = append(ghosts, p)
		}
	}
	if len(ghosts) == 0 {
		return ""
	}
	// Limit to the worst 3 by symbol count so the advisory stays
	// scannable. projects is pre-sorted by symbol count desc, but
	// ghosts inherits that ordering since we walked in order.
	if len(ghosts) > 3 {
		ghosts = ghosts[:3]
	}
	var names []string
	for _, g := range ghosts {
		if g.Edges == 0 {
			names = append(names, fmt.Sprintf("%q (%d symbols, %d files, 0 edges)",
				g.Name, g.Symbols, g.Files))
		} else {
			names = append(names, fmt.Sprintf("%q (%d symbols, %d files, %d edges — ratio %.6f)",
				g.Name, g.Symbols, g.Files, g.Edges,
				float64(g.Edges)/float64(g.Symbols)))
		}
	}
	msg := fmt.Sprintf("project%s with substantial symbols but vanishingly few edges (ghost-extraction signature, #815): %s. ",
		pluralS(len(ghosts)), strings.Join(names, "; "))
	msg += "Symbols were extracted but the resolver phase produced no real graph — `trace` / `query` over these projects will silently return zero rows. " +
		"Remediation: re-index from a fresh CWD (`pincher index <path>`) and check `doctor`'s extraction_failures list for the underlying cause."
	return msg
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// branchDriftAdvisory returns a human-readable advisory when one or
// more projects' on-disk git branch has drifted from the branch they
// were last indexed against. #1303 Phase 2a. The mismatch is the
// silent-stale-results footgun the column was added to catch: index
// against branch `main`, check out `feat/foo`, query — pincher returns
// `main`'s symbol byte-offsets, which point at the wrong spans in the
// `feat/foo` working tree.
//
// Lookup is per-project `git rev-parse --abbrev-ref HEAD` with a 1s
// per-project timeout, capped at the first 30 projects so a busy
// install doesn't slow the doctor query. The cap matches the spirit
// of payloadOutlierAdvisory's bounded scan — 30 covers every real
// install, anything bigger is the user-pincher dogfood case where
// re-running doctor is acceptable.
//
// Projects with empty `current_branch` (indexed before #1303 Phase 2a
// landed) are skipped — re-index will populate the value and the
// advisory becomes reliable. Projects whose on-disk path isn't a git
// repo are also skipped (the rev-parse fails and we have nothing to
// compare).
//
// Output: one line per drifted project, sorted by project name for
// deterministic snapshot tests, capped at 5 in the rendered output
// with a "+N more" suffix when there are more.
func branchDriftAdvisory(projects []db.Project) string {
	const perProjectTimeout = 1 * time.Second
	const maxProjectsToProbe = 30
	const maxToShow = 5

	// #1671 v0.87: build the nested-projects set so a project that's
	// physically inside ANOTHER indexed project (e.g.,
	// internal/supervisor/cmd/probe nested inside pincher-repo)
	// can be skipped — git -C against the nested path resolves via
	// the parent's .git and false-reports the parent's branch as
	// the nested project's, producing a phantom drift advisory.
	nestedNorms := make(map[string]bool, len(projects))
	pathNorms := make([]string, 0, len(projects))
	for _, p := range projects {
		if p.Path != "" {
			pathNorms = append(pathNorms, normalizePathForNesting(p.Path))
		}
	}
	for _, inner := range pathNorms {
		for _, outer := range pathNorms {
			if inner == outer || outer == "" {
				continue
			}
			if strings.HasPrefix(inner, outer+"/") {
				nestedNorms[inner] = true
				break
			}
		}
	}

	type drift struct {
		Name        string
		LastIndexed string
		OnDisk      string
	}
	var drifts []drift

	probed := 0
	for _, p := range projects {
		if probed >= maxProjectsToProbe {
			break
		}
		if p.CurrentBranch == "" {
			continue // pre-#1303-Phase-2a row; skip
		}
		// #1665 v0.87: testdata corpora and worktree-staging projects
		// don't have meaningful branch drift — their content is
		// static testdata, not real working trees. The advisory's
		// remediation (`pincher index <path>`) would just churn the
		// index without fixing anything. Same suppression list as
		// the nested-project advisory (#1644).
		if branchDriftSuppressedPath(p.Path) {
			continue
		}
		// #1667 v0.87: skip projects whose on-disk path no longer
		// exists OR no longer points to a directory. Both are
		// dead-path ghosts that should be cleaned up by `list
		// prune_dead=true`, not by `pincher index <path>` — the
		// suggested remediation errors out on a missing path and
		// is nonsense for a file. The IsDir check catches the case
		// where a project's path got replaced by a file of the
		// same name (e.g., a `probe` directory replaced by a
		// `probe.exe` executable that git -C then resolves via
		// the parent's .git).
		if fi, err := os.Stat(p.Path); err != nil || !fi.IsDir() {
			continue
		}
		// #1671 v0.87: skip nested projects whose path lives under
		// ANOTHER indexed project. git -C against the nested path
		// resolves via the parent's .git and false-reports the
		// parent's branch as the nested project's, producing a
		// phantom drift on every parent branch switch. The
		// nested-project advisory (#1209) already surfaces these as
		// a doubled-DB-load concern with its own remediation;
		// claiming "drift" here would be a duplicate signal that
		// channels users toward the wrong fix (`pincher index
		// <nested-path>` would just re-extract files already
		// indexed by the parent).
		if nestedNorms[normalizePathForNesting(p.Path)] {
			continue
		}
		probed++
		ctx, cancel := context.WithTimeout(context.Background(), perProjectTimeout)
		out, err := exec.CommandContext(ctx, "git", "-C", p.Path, "rev-parse", "--abbrev-ref", "HEAD").Output()
		cancel()
		if err != nil {
			continue
		}
		onDisk := strings.TrimSpace(string(out))
		if onDisk == "" || onDisk == p.CurrentBranch {
			continue
		}
		// Detached HEAD: --abbrev-ref returns "HEAD". Fall back to
		// the short SHA so the comparison against indexer-detected
		// "abc1234" still works.
		if onDisk == "HEAD" {
			ctx2, cancel2 := context.WithTimeout(context.Background(), perProjectTimeout)
			sha, err := exec.CommandContext(ctx2, "git", "-C", p.Path, "rev-parse", "--short", "HEAD").Output()
			cancel2()
			if err != nil {
				continue
			}
			onDisk = strings.TrimSpace(string(sha))
			if onDisk == p.CurrentBranch {
				continue
			}
		}
		drifts = append(drifts, drift{Name: p.Name, LastIndexed: p.CurrentBranch, OnDisk: onDisk})
	}

	if len(drifts) == 0 {
		return ""
	}

	sort.Slice(drifts, func(i, j int) bool { return drifts[i].Name < drifts[j].Name })

	var b strings.Builder
	b.WriteString("Branch drift on ")
	if len(drifts) == 1 {
		b.WriteString("1 project: ")
	} else {
		fmt.Fprintf(&b, "%d projects: ", len(drifts))
	}
	show := drifts
	extra := 0
	if len(show) > maxToShow {
		extra = len(show) - maxToShow
		show = show[:maxToShow]
	}
	for i, d := range show {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s (indexed=%s, on-disk=%s)", d.Name, d.LastIndexed, d.OnDisk)
	}
	if extra > 0 {
		fmt.Fprintf(&b, "; +%d more", extra)
	}
	b.WriteString(". The on-disk branch differs from the one the project was last indexed against, " +
		"so symbol byte-offsets may point at the wrong spans for the current working tree. " +
		"Run `pincher index <path>` against each drifted project to refresh. See #1303.")
	return b.String()
}

// walBloatAdvisory returns a human-readable health advisory when the
// pincher WAL has grown well past its configured 256 MiB
// journal_size_limit, or "" when it's fine. #1206 v0.66 DOGFOOD: WAL
// bloat is the hidden symptom of checkpoint failure under sustained
// concurrent indexing pressure (readers pin the WAL across the
// truncate). The user doesn't see it until vacuum or until a tool
// call latency blows out (the WAL has to be touched on every read).
//
// Threshold: 512 MiB OR > 10% of the DB. Either is large enough to
// merit surfacing; smaller WALs aren't a problem worth a warning.
// Remediation: `pincher vacuum` runs a wal_checkpoint(TRUNCATE)
// + VACUUM cycle, which reclaims the WAL space. Tracking issue
// #1206 / related #1149 (vacuum's own wal_reader_busy advisory).
// fts5FragmentationAdvisory returns a human-readable advisory when
// any per-corpus FTS5 vtab's data/idx ratio crosses the threshold,
// or "" when every corpus is within the healthy band. #1612 v0.87:
// gives the user a proactive signal that BM25 search latency is
// degrading without waiting for them to notice a slow `search`.
// Remediation is `pincher rebuild_fts` which is exposed via both the
// MCP tool and the CLI subcommand.
func fts5FragmentationAdvisory(rows []db.FTS5CorpusFragmentation) string {
	type fragged struct {
		corpus string
		ratio  float64
		data   int64
		idx    int64
	}
	var bad []fragged
	for _, r := range rows {
		if r.NeedsRebuild {
			bad = append(bad, fragged{r.Corpus, r.Ratio, r.DataRows, r.IdxRows})
		}
	}
	if len(bad) == 0 {
		return ""
	}
	// Build the per-corpus phrase. Skip the worst-first sort — the
	// per-corpus order is stable across the three known names and
	// presenting them in canonical order (code/config/docs) is the
	// least surprising shape for a reader inspecting a doctor report.
	var parts []string
	for _, b := range bad {
		parts = append(parts, fmt.Sprintf("%s corpus is %.1fx fragmented (%d data rows / %d index rows)",
			b.corpus, b.ratio, b.data, b.idx))
	}
	joined := strings.Join(parts, "; ")
	return fmt.Sprintf(
		"FTS5 fragmentation high — %s. Healthy ratio is 1–3x; values above %.0fx indicate accumulated "+
			"micro-segments that slow BM25 ranking. Remediation: `pincher rebuild_fts` (or call the "+
			"`rebuild_fts` MCP tool) — drops and rebuilds every per-corpus vtab from `symbols` in a "+
			"single transaction. Cost scales with symbol count: seconds on small repos, minutes on "+
			"large ones. Safe to run during normal operation — readers are blocked only for the "+
			"transaction's duration. See #1612.",
		joined, db.FTS5FragmentationThreshold)
}

// settingsFloodAdvisory returns a human-readable advisory when a
// project's symbol surface is dominated by Setting-kind symbols, or ""
// when every project looks healthy. This is the signature of indexed
// data/model artifacts: observed in the wild as data/*.json model files
// (2.5 MB each — under the 4 MB per-file cap, so the size gate never
// fired) becoming 27,672 junk Setting symbols, 96% of the project.
// Search ranking, the dashboard, and aggregate stats all drown in the
// noise, and pre-.pincherignore there was no user-facing way out.
//
// Thresholds: > 1000 total symbols (below that, a genuinely
// config-heavy repo — dotfiles, Ansible inventory — can legitimately
// be all Settings and small enough not to matter) AND Setting share
// strictly above 80% (real code repos with generous YAML/TOML config
// sit well below this; the observed pathological case was 96%).
//
// settingCounts is keyed by project ID (from
// db.Store.SettingSymbolCountsByProject — one aggregate query).
// projects must be sorted by symbol count descending (handleDoctor
// already does this) so the worst offenders surface first.
func settingsFloodAdvisory(projects []doctorProjectSummary, settingCounts map[string]int) string {
	const symThreshold = 1000
	const maxShare = 0.80
	var flooded []string
	for _, p := range projects {
		if p.Symbols <= symThreshold {
			continue
		}
		settings := settingCounts[p.ID]
		share := float64(settings) / float64(p.Symbols)
		if share <= maxShare {
			continue
		}
		flooded = append(flooded, fmt.Sprintf("%q (%d of %d symbols are Settings — %.0f%%)",
			p.Name, settings, p.Symbols, share*100))
		// Cap at the worst 3 so the advisory stays scannable.
		if len(flooded) == 3 {
			break
		}
	}
	if len(flooded) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"project%s dominated by Setting symbols (data-artifact flood signature): %s. "+
			"This usually means generated config/data files (JSON model artifacts, exported fixtures) are being "+
			"indexed as thousands of low-signal Setting symbols, drowning out real code in search and stats. "+
			"Remediation: add a .pincherignore file (gitignore semantics) at the project root covering the "+
			"artifact paths (e.g. `data/` or `*.generated.json`), then re-index — symbols of newly-ignored "+
			"files are garbage-collected automatically on the next index run.",
		pluralS(len(flooded)), strings.Join(flooded, "; "))
}

func walBloatAdvisory(dbSizeBytes, walSizeBytes int64) string {
	const absThreshold = 512 << 20 // 512 MiB
	// The percent rule (WAL > 10% of DB) is only meaningful once the
	// DB itself is large enough that the ratio reflects real bloat.
	// Test DBs and fresh installs land at a few KB of data; 1 MB WAL
	// is normal there and not worth surfacing. Skip the percent rule
	// below 100 MiB DB size.
	const minDBForPercent = 100 << 20 // 100 MiB
	percentBloat := dbSizeBytes >= minDBForPercent && walSizeBytes*10 > dbSizeBytes
	if walSizeBytes < absThreshold && !percentBloat {
		return ""
	}
	walMB := float64(walSizeBytes) / (1 << 20)
	msg := fmt.Sprintf("WAL file is %.0f MB — past the 256 MB journal_size_limit soft cap.", walMB)
	if dbSizeBytes > 0 {
		msg += fmt.Sprintf(" That's %.0f%% of the DB (%.1f GB).",
			float64(walSizeBytes)/float64(dbSizeBytes)*100,
			float64(dbSizeBytes)/(1<<30))
	}
	msg += " Under sustained indexing pressure, checkpoints can't always truncate the WAL " +
		"(busy readers pin pages across the cycle). Every tool call touches the WAL first, " +
		"so a large WAL inflates latency on otherwise-cheap reads. Remediation: run `pincher vacuum` " +
		"to force a checkpoint + truncate + reclaim cycle. See #1206."
	return msg
}

// largeDBAdvisory returns a human-readable health advisory when the
// pincher DB is pathologically large, or "" when it's fine. #732:
// failure-as-pedagogy for the diagnostic itself — `doctor` used to
// report db_size_bytes as a bare number, so a 4.7 GB store looked no
// different from a 4.7 MB one. A multi-GB store is almost always stale
// projects accumulated by old binaries, not live data. `projects` must
// be sorted by symbol count descending (handleDoctor already does this)
// so projects[0] is the heaviest — naming it makes the advisory
// concrete.
func largeDBAdvisory(dbSizeBytes int64, projects []doctorProjectSummary) string {
	const threshold = 1 << 30 // 1 GiB
	if dbSizeBytes <= threshold {
		return ""
	}
	msg := fmt.Sprintf("database is %.1f GB — unusually large. This is almost always "+
		"stale projects accumulated by older binaries, not live data.",
		float64(dbSizeBytes)/(1<<30))
	if len(projects) > 0 {
		h := projects[0]
		msg += fmt.Sprintf(" Heaviest project: %q (%d symbols across %d files).",
			h.Name, h.Symbols, h.Files)
	}
	msg += " Remediation: `list prune_dead=true` removes projects whose on-disk path is gone; " +
		"`pincher project prune-stale` drops projects indexed by an old schema and untouched for " +
		"30+ days. SQLite does not shrink the file on row deletion, so run `pincher vacuum` " +
		"afterward to actually reclaim the space."
	return msg
}

// reclaimableDBAdvisory returns a human-readable health advisory when
// SQLite's freelist has enough free pages to make an explicit VACUUM
// worthwhile. This catches the post-prune / post-reindex shape where the
// DB is below the "large DB" threshold but still has substantial stranded
// space inside the file.
func reclaimableDBAdvisory(dbSizeBytes, reclaimableBytes int64) string {
	if dbSizeBytes <= 0 || reclaimableBytes < db.VacuumReclaimThresholdBytes {
		return ""
	}
	pct := float64(reclaimableBytes) / float64(dbSizeBytes) * 100
	return fmt.Sprintf("database has %s of reclaimable free pages (%.0f%% of the %s DB). "+
		"SQLite keeps freed pages inside the file after deletes, FTS rebuilds, and full re-index clears until VACUUM rewrites it. "+
		"Remediation: run `pincher vacuum` or `pincher doctor --fix` to reclaim the space and refresh planner statistics.",
		db.FormatSize(int(reclaimableBytes)), pct, db.FormatSize(int(dbSizeBytes)))
}

// payloadOutlierAdvisory returns a human-readable health advisory when
// a tool's max response_bytes over the trailing 7d window is many
// multiples of its average AND the absolute max is large enough to
// matter (≥100 KB). Same pattern as largeDBAdvisory: a single big
// response per week is invisible in averages but is the calls that
// occasionally blow up token bills — surfacing it lets the user
// decide if `guide` (or whichever tool) needs a per-call cap.
//
// Thresholds chosen for typical agent workloads:
//
//	ratio ≥ 10×   "loud outlier" relative to that tool's normal payload
//	max  ≥ 100 KB the absolute floor for "this would meaningfully
//	               consume an agent's context window"
//
// Returns "" when nothing crosses both bars — silent on healthy data.
type payloadOutlierHit struct {
	tool  string
	ratio float64
	max   int64
	avg   float64
}

func payloadOutlierAdvisory(rows []db.ToolCallPayloadRow) string {
	const (
		minRatio = 10.0
		minMax   = int64(100 * 1024)
	)
	hits := []payloadOutlierHit{}
	for _, r := range rows {
		if r.MaxBytes < minMax || r.AvgBytes <= 0 {
			continue
		}
		ratio := float64(r.MaxBytes) / r.AvgBytes
		if ratio < minRatio {
			continue
		}
		hits = append(hits, payloadOutlierHit{tool: r.Tool, ratio: ratio, max: r.MaxBytes, avg: r.AvgBytes})
	}
	if len(hits) == 0 {
		return ""
	}
	// Top 3 by ratio so the advisory stays scannable.
	sort.Slice(hits, func(i, j int) bool { return hits[i].ratio > hits[j].ratio })
	if len(hits) > 3 {
		hits = hits[:3]
	}
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, fmt.Sprintf("%s (max %s, avg %s, %.1f× spread)",
			h.tool, fmtBytesHuman(h.max), fmtBytesHuman(int64(h.avg)), h.ratio))
	}
	return "Payload outliers in the last 7 days — one or more tools occasionally returned a much larger " +
		"response than their average. These are the calls that blow up agent context windows: " +
		strings.Join(parts, "; ") +
		". Remediation: inspect the offending calls via /v1/tool-payload-stats or the dashboard's " +
		"Response Payload Size panel; " + payloadOutlierRemediation(hits) + "."
}

func payloadOutlierRemediation(hits []payloadOutlierHit) string {
	hasTool := func(name string) bool {
		for _, h := range hits {
			if h.tool == name {
				return true
			}
		}
		return false
	}
	if hasTool("context") {
		return "for context outliers, pass lite=true when source-only is enough, or fields=symbol / fields=symbol,callees to drop heavy dependency sections"
	}
	return "consider narrowing the query (lower min_confidence, smaller k, specific path filter) on calls to that tool to bound payload size"
}

// toolMixStuckAdvisory returns a human-readable health advisory when
// the agent has been stuck in a narrow tool loop over the trailing
// window — entropy < 0.5 bits AND ≥100 total calls AND top-1 share
// > 80%. The triple-gate is deliberate: low entropy alone on a
// fresh install is normal (small sample); without a call-volume
// floor we'd false-positive every empty session.
//
// "Stuck" here means "essentially calling one tool" — the agent is
// repeating a probe rather than triangulating across tools, which
// is usually a query that's not converging. The advisory points the
// user at the dashboard's Tool-Mix Health panel for context.
func toolMixStuckAdvisory(rows []db.ToolCallTallyRow) string {
	const (
		minCalls     = int64(100)
		maxEntropy   = 1.0 // strictly less than this trips
		minTop1Share = 0.80
	)
	var total int64
	for _, r := range rows {
		total += r.CallCount
	}
	if total < minCalls || len(rows) == 0 {
		return ""
	}
	// Shannon entropy and top-1 share. Rows arrive sorted by
	// call_count DESC from ToolCallStatsByTool, so rows[0] is the
	// top tool.
	var entropy float64
	for _, r := range rows {
		p := float64(r.CallCount) / float64(total)
		if p > 0 {
			entropy += -p * (math.Log(p) / math.Ln2)
		}
	}
	top1Share := float64(rows[0].CallCount) / float64(total)
	if entropy >= maxEntropy || top1Share < minTop1Share {
		return ""
	}
	return fmt.Sprintf(
		"Tool-mix entropy is %.2f bits over %d calls — the agent is essentially repeating one tool "+
			"(%q at %.0f%% of all calls). Usually means a query that is not converging: the agent keeps "+
			"re-issuing the same shape rather than triangulating. Inspect via the dashboard's "+
			"Tool-Mix Health panel; consider whether the agent needs a wider initial probe "+
			"(architecture / search) before drilling.",
		entropy, total, rows[0].Tool, top1Share*100,
	)
}

// fmtBytesHuman formats a byte count with a binary unit suffix.
// Mirrors the dashboard's fmtBytes JS helper so the CLI/MCP doctor
// output reads the same as the dashboard surface.
func fmtBytesHuman(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024.0)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024.0*1024.0))
	}
}

// nestedProjectAdvisory returns a human-readable advisory when one
// indexed project's path is a strict subdirectory of another's. Same
// source files end up indexed under two different project IDs, which
// (a) doubles disk pressure on the symbols/edges/FTS5 tables, and (b)
// makes audit-shape queries against the parent project surface symbols
// that the user thinks of as belonging to the child project — even
// after the per-project collision detector (#1209 part 1) correctly
// keeps the rows distinct, the user-visible duplication is the
// confusing part.
//
// Discovery context: v0.66 DOGFOOD found `warp_rc` (1.45M syms) and
// `warp-fork` (1.07M syms, located at `warp_rc/warp-fork/`) both
// registered. The user did not realize the inner project was a strict
// subdir of the outer; doctor's advisory list said nothing about it.
//
// Algorithm: for every pair (A, B) where A != B, B is nested under A
// iff normalize(B.Path) has prefix normalize(A.Path)+sep. Normalize
// case-folds and forward-slash-normalizes for Windows portability.
// Path-separator-after-parent guard avoids `warp_rc` falsely matching
// `warp_rc_fork`. The advisory names the inner project (the
// "redundant" one); the outer was almost always indexed first and is
// the one the user wants to keep.
//
// Caps at the worst 3 pairs by inner-project symbol count so the
// advisory stays scannable. #1209.
func nestedProjectAdvisory(projects []doctorProjectSummary) string {
	type nestedPair struct {
		inner doctorProjectSummary
		outer doctorProjectSummary
	}
	var pairs []nestedPair
	normalized := make([]string, len(projects))
	for i, p := range projects {
		normalized[i] = normalizePathForNesting(p.Path)
	}
	for i, inner := range projects {
		ni := normalized[i]
		if ni == "" {
			continue
		}
		// Find the most-specific outer that contains inner. Multiple
		// nested levels are possible (`a/`, `a/b/`, `a/b/c/`); naming
		// the immediate parent (`a/b/` for `a/b/c/`) is most actionable.
		var bestOuter *doctorProjectSummary
		var bestOuterLen int
		for j, outer := range projects {
			if i == j {
				continue
			}
			no := normalized[j]
			if no == "" {
				continue
			}
			// Strict prefix with trailing separator to avoid
			// `warp_rc` matching `warp_rc_fork`. ni and no are
			// already lowercased + forward-slash; the trailing
			// "/" sentinel is the inner's separator boundary.
			if len(ni) > len(no)+1 &&
				strings.HasPrefix(ni, no+"/") &&
				len(no) > bestOuterLen {
				outerCopy := outer
				bestOuter = &outerCopy
				bestOuterLen = len(no)
			}
		}
		if bestOuter != nil {
			// #1644: suppress when the nesting is intentional (testdata
			// corpus fixtures, worktree convention dirs). The advisory's
			// remediation suggestion (`pincher project rm <inner>`) is
			// the wrong action there — removing the corpus snapshot
			// fixtures would break `make corpus-test`.
			if isIntentionallyNested(normalized[i], normalizePathForNesting(bestOuter.Path)) {
				continue
			}
			pairs = append(pairs, nestedPair{inner: inner, outer: *bestOuter})
		}
	}
	if len(pairs) == 0 {
		return ""
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].inner.Symbols > pairs[j].inner.Symbols
	})
	if len(pairs) > 3 {
		pairs = pairs[:3]
	}
	var names []string
	for _, p := range pairs {
		names = append(names, fmt.Sprintf("%q (%d symbols) is indexed inside %q",
			p.inner.Name, p.inner.Symbols, p.outer.Name))
	}
	msg := fmt.Sprintf("nested project registration%s: %s. ",
		pluralS(len(pairs)), strings.Join(names, "; "))
	msg += "Same source files are indexed under both project IDs, " +
		"doubling DB load and surfacing duplicates in cross-project queries. " +
		"Remediation: choose one root (usually the outer), `list prune_dead=true` " +
		"after deleting the redundant project from disk, or use `pincher project rm` " +
		"on the inner project if it remains on disk. See #1209."
	return msg
}

// branchDriftSuppressedPath returns true when a project's path
// matches a convention where the "branch drift" advisory should
// stay silent — testdata corpora (#1644) and worktree-staging
// dirs. Same suppression segments as isIntentionallyNested, but
// keyed on a SINGLE path (no inner/outer relationship): a project
// whose own path lives under `testdata/corpus/` etc. doesn't have
// meaningful branch drift independent of any outer project. #1665.
func branchDriftSuppressedPath(path string) bool {
	norm := normalizePathForNesting(path)
	for _, seg := range []string{
		"/testdata/corpus/", "testdata/corpus/",
		"/testdata/__fixtures__/", "testdata/__fixtures__/",
		"/.atrium/work/", ".atrium/work/",
	} {
		if strings.Contains(norm, seg) {
			return true
		}
	}
	return false
}

// isIntentionallyNested returns true when the inner project's path,
// relative to the outer project's path, contains a segment that marks
// the nesting as deliberate (pinned testdata corpora, worktree convention
// dirs). The nested-project advisory's remediation — `pincher project rm
// <inner>` — would be actively harmful in those cases (would break
// `make corpus-test` snapshot gates, would interfere with the worktree
// tooling that depends on the nested registration).
//
// Suppression patterns:
//   - testdata/corpus/      pincher's pinned corpus convention; matches
//     k8s-ops, terraform-stack, probe, etc.
//     (#1644).
//   - testdata/__fixtures__/  broader trace include_fixtures convention.
//   - .atrium/work/         slopbuster worktree convention.
//
// Both arguments must already be normalized by normalizePathForNesting.
func isIntentionallyNested(innerNorm, outerNorm string) bool {
	if !strings.HasPrefix(innerNorm, outerNorm+"/") {
		return false
	}
	rel := innerNorm[len(outerNorm)+1:]
	if rel == "" {
		return false
	}
	rel = "/" + rel + "/"
	suppressSegments := []string{
		"/testdata/corpus/",
		"/testdata/__fixtures__/",
		"/.atrium/work/",
	}
	for _, seg := range suppressSegments {
		if strings.Contains(rel, seg) {
			return true
		}
	}
	return false
}

// normalizePathForNesting lowercases and forward-slash-normalizes a
// project path for case-insensitive prefix comparison. Windows uses
// `D:\ClaudeCode\warp_rc` while macOS / Linux use `/home/u/warp_rc`;
// both need to compare against their nested children under one rule.
// Returns "" for empty input so the caller can skip projects with no
// recorded path.
func normalizePathForNesting(path string) string {
	if path == "" {
		return ""
	}
	low := strings.ToLower(path)
	low = strings.ReplaceAll(low, `\`, "/")
	// Trim trailing slash so the prefix-with-"/"+sep check below is
	// unambiguous regardless of whether the stored path ended in a
	// separator.
	return strings.TrimRight(low, "/")
}

// handleDoctor builds a structured diagnostic report from the live DB:
// schema version, file sizes, project staleness, recent extraction
// failures, recent slow queries. Read-only — no DB mutations.
func (s *Server) handleDoctor(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	rawLookback := intArg(args, "lookback_hours", 168)
	rawTop := intArg(args, "top", 10)
	lookbackHours := rawLookback
	top := rawTop
	// #1015: surface input clamps so caller sees what defaults landed.
	// Same shape as search (#879) / neighborhood (#1013) — silent clamps
	// leave the caller thinking they got what they asked for.
	var clampWarnings []string
	if lookbackHours <= 0 {
		if _, present := args["lookback_hours"]; present {
			clampWarnings = append(clampWarnings,
				fmt.Sprintf("doctor: lookback_hours=%d clamped to 168 (must be positive)", rawLookback))
		}
		lookbackHours = 168
	}
	if top <= 0 {
		if _, present := args["top"]; present {
			clampWarnings = append(clampWarnings,
				fmt.Sprintf("doctor: top=%d clamped to 10 (must be positive)", rawTop))
		}
		top = 10
	}
	// #1016 / #1054: upper-bound clamp. top governs three list caps
	// (projects, extraction_failures, slow_queries) AND the per-project
	// ListExtractionFailures call. Pre-#1016 top=99999 on a multi-project
	// install produced a 506 KB response that blew the MCP per-call
	// token cap. #1016 capped at 500 to mirror search/neighborhood, but
	// doctor returns three sections at `top` each PLUS per-row detail
	// (file paths, multi-line stack traces) — at top=500 the response
	// still ran ~218 KB and exceeded the MCP cap. Dogfood-discovered:
	// the agent gets "result exceeds maximum allowed tokens" with no
	// recovery affordance. Drop to 50 — three sections × 50 rows with
	// ~400-byte rows lands well inside the per-call budget. For deeper
	// enumeration use `list` (per-project pagination) or pinchQL
	// queries against extraction_failures / slow_queries directly.
	const maxTop = 50
	if top > maxTop {
		clampWarnings = append(clampWarnings,
			fmt.Sprintf("doctor: top=%d clamped to %d (max). The response carries 3 sections (projects, extraction_failures, slow_queries) each capped at `top` — higher caps blow the MCP per-call token budget. Use `list` for paginated project enumeration if you need more.", rawTop, maxTop))
		top = maxTop
	}

	// #1401: optional substring filter against project name/id. Same
	// match shape as `pincher project rm` / `pincher verify --project`
	// (case-insensitive substring against name OR id). When set, the
	// projects + extraction_failures sections are filtered to matched
	// project_ids; the store-wide advisories (large-DB, ghost-project,
	// nested-project, WAL bloat, branch drift) intentionally still walk
	// every project — they're database-level signals, not project-scoped.
	projectFilter, _ := args["project"].(string)
	if trimmed := strings.TrimSpace(projectFilter); trimmed != "" && filepath.Clean(trimmed) == "." && s.sessionID != "" {
		projectFilter = s.sessionID
	}

	data := map[string]any{
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"binary_version": s.version,
		"lookback_hours": lookbackHours,
	}
	if projectFilter != "" {
		data["project_filter"] = projectFilter
	}
	for _, w := range clampWarnings {
		attachWarning(data, w)
	}

	var schemaVersion int
	if err := s.store.DB().QueryRow(`SELECT version FROM schema_version`).Scan(&schemaVersion); err != nil {
		schemaVersion = 0
	}
	data["schema_version"] = schemaVersion

	// #1497: surface what the most recent schema migration did, so users
	// can see whether their last `make install` + `/mcp` reconnect bumped
	// only "Nothing"-class migrations (no re-extraction needed) or
	// included an "All"-class one (the binary's extractor changes
	// require a full reindex to surface correctly). Empty when no
	// migrations ran this startup (DB already at current schema).
	inv, migFrom, migTo := s.store.LastStartupMigrationInvalidates()
	if migFrom != migTo {
		migInfo := map[string]any{
			"from_version":          migFrom,
			"to_version":            migTo,
			"requires_full_reindex": inv.All,
		}
		if len(inv.Languages) > 0 {
			migInfo["affected_languages"] = inv.Languages
		}
		data["startup_migrations"] = migInfo
		if inv.All {
			attachWarning(data, fmt.Sprintf(
				"doctor: this binary's schema bump (v%d → v%d) included migrations that invalidate previously-extracted data — run `pincher index --force <path>` on each project to refresh; otherwise some graph queries may surface stale results until the watcher's next pass.",
				migFrom, migTo,
			))
		}
	}

	dbPath := s.store.Path
	var dbSizeBytes, walSizeBytes int64
	if info, err := os.Stat(dbPath); err == nil {
		dbSizeBytes = info.Size()
	}
	if info, err := os.Stat(dbPath + "-wal"); err == nil {
		walSizeBytes = info.Size()
	}
	data["db_size_bytes"] = dbSizeBytes
	data["wal_size_bytes"] = walSizeBytes

	projects := []doctorProjectSummary{}
	plist, err := s.store.ListProjects()
	currentSchema := db.CurrentSchemaVersion()
	// #1220: one extra reader-pool SELECT (two grouped SUMs against
	// symbols/edges) so each project summary can carry a best-effort
	// db_bytes_estimate. Missing entries (e.g. a freshly upserted
	// project with no symbols yet) default to 0, which is honest.
	projectBytes, _ := s.store.EstimateProjectBytes()
	// #1401 / #1404: pre-build the set of project_ids that pass the
	// tiered filter so the projects loop AND the extraction_failures
	// section share one membership predicate. Resolution order matches
	// `pincher project rm`: exact id (case-sensitive) → exact name
	// (case-insensitive) → substring on name OR id. Pre-#1404 the
	// flat substring check over-matched on nested project IDs
	// containing the parent path. matchedProjectIDs is nil when no
	// filter is set (means "all"); non-nil empty means "filter
	// applied, nothing matched".
	matchedProjectIDs := doctorMatchedProjectIDsForFilter(plist, projectFilter)
	if err == nil {
		for _, p := range plist {
			if matchedProjectIDs != nil {
				if _, ok := matchedProjectIDs[p.ID]; !ok {
					continue
				}
			}
			summary := doctorProjectSummary{
				ID:                   p.ID,
				Name:                 p.Name,
				Path:                 p.Path,
				Files:                p.FileCount,
				Symbols:              p.SymCount,
				Edges:                p.EdgeCount,
				DBBytesEstimate:      projectBytes[p.ID],
				IndexedAt:            p.IndexedAt.Format(time.RFC3339),
				SchemaVersionAtIndex: p.SchemaVersionAtIndex,
				BinaryVersion:        p.BinaryVersion,
			}
			summary.Stale, summary.StaleReason = projectSchemaStaleness(p.SchemaVersionAtIndex, currentSchema)
			summary.IndexState, summary.IndexStartedAt = projectIndexStateFields(p.IndexState, p.IndexStartedAt)
			projects = append(projects, summary)
		}
		sort.Slice(projects, func(i, j int) bool {
			return projects[i].Symbols > projects[j].Symbols
		})
	}
	// #575: cap projects to `top` to bound the response size on
	// multi-project installs. Pre-fix a 125-project install + default
	// top=10 returned all 125 projects (≈19 KB) plus all 198 failures
	// (≈80 KB) for a total ≈119 KB — exceeded the MCP per-call cap.
	// Sorted-by-symbol-count-desc above means we keep the largest
	// (most likely to surface a problem) first.
	if len(projects) > top {
		data["projects_truncated"] = len(projects) - top
		projects = projects[:top]
	}
	data["projects"] = projects
	allProjects := []doctorProjectSummary{}
	if err == nil {
		for _, p := range plist {
			summary := doctorProjectSummary{
				ID:                   p.ID,
				Name:                 p.Name,
				Path:                 p.Path,
				Files:                p.FileCount,
				Symbols:              p.SymCount,
				Edges:                p.EdgeCount,
				SchemaVersionAtIndex: p.SchemaVersionAtIndex,
				BinaryVersion:        p.BinaryVersion,
			}
			summary.Stale, summary.StaleReason = projectSchemaStaleness(p.SchemaVersionAtIndex, currentSchema)
			summary.IndexState, summary.IndexStartedAt = projectIndexStateFields(p.IndexState, p.IndexStartedAt)
			allProjects = append(allProjects, summary)
		}
		sort.Slice(allProjects, func(i, j int) bool { return allProjects[i].Symbols > allProjects[j].Symbols })
	}

	// #732: failure-as-pedagogy for the diagnostic itself. doctor used
	// to report db_size_bytes as a bare number — a 4.7 GB store looked
	// no different from a 4.7 MB one. Surface an actionable advisory
	// when the DB is pathologically large. Use the unfiltered project
	// list so `project=...` only scopes display sections, not store-wide
	// health signals.
	advisories := []string{}
	if a := largeDBAdvisory(dbSizeBytes, allProjects); a != "" {
		advisories = append(advisories, a)
	}
	if reclaimableBytes, err := s.store.EstimateReclaimableBytes(); err == nil {
		if a := reclaimableDBAdvisory(dbSizeBytes, reclaimableBytes); a != "" {
			advisories = append(advisories, a)
		}
	}
	// #1206 v0.66 DOGFOOD: WAL bloat advisory. Pincher's WAL is
	// supposed to be bounded by journal_size_limit=256 MiB (set in
	// the schema config) + CheckpointTruncate() on every Index()
	// tail. journal_size_limit is a SOFT cap: SQLite honors it
	// after a successful checkpoint, but if checkpoints can't
	// complete (busy readers pin the WAL across the truncate), the
	// WAL grows past the limit. Real-world observation on an 11GB
	// DB: WAL reached 2.3GB (9x the configured limit) — caller has
	// no signal until vacuum surfaces the wal_reader_busy bit
	// (#1149), too late to remediate proactively.
	//
	// Threshold: 512 MiB OR > 10% of the DB. Either is large
	// enough that the user should know; smaller WALs aren't a
	// problem worth surfacing. Bare number with a remediation
	// hint, same shape as largeDBAdvisory.
	if a := walBloatAdvisory(dbSizeBytes, walSizeBytes); a != "" {
		advisories = append(advisories, a)
	}
	// #1009: ghost-project advisory. Walks the full project list (not
	// the filtered or `top`-truncated one) so a ghost project outside the
	// display slice is still surfaced. #1209: nested-project advisory uses
	// the same full-list pass — the inner project is often ranked below
	// the outer by symbol count, so any filtered/truncated view risks
	// dropping the pair.
	{
		if a := ghostProjectAdvisory(allProjects); a != "" {
			advisories = append(advisories, a)
		}
		if a := nestedProjectAdvisory(allProjects); a != "" {
			advisories = append(advisories, a)
		}
		if a := staleIndexRunningAdvisory(allProjects, time.Now().UTC()); a != "" {
			advisories = append(advisories, a)
		}
		if a := projectSchemaDriftAdvisory(allProjects, currentSchema); a != "" {
			advisories = append(advisories, a)
		}
	}
	// #635 v0.67 follow-up: payload-outlier advisory. Surfaces tools
	// whose worst-case response is many multiples of their average AND
	// large enough to meaningfully consume an agent context window.
	// Pull at most 200 rows — bounded so a noisy install doesn't slow
	// the doctor query, and big enough to cover every real tool catalog.
	if payloadRows, err := s.store.ToolCallPayloadSizeByTool(7*24*60*60, 200); err == nil {
		if a := payloadOutlierAdvisory(payloadRows); a != "" {
			advisories = append(advisories, a)
		}
	}
	// #635 v0.67 follow-up: tool-mix entropy advisory. Same data source
	// as the dashboard entropy panel; fires only on truly stuck loops
	// (entropy <1.0 bits AND ≥100 calls AND top-1 share >80%) so the
	// advisory stays silent on legitimately narrow workloads.
	if tallyRows, err := s.store.ToolCallStatsByTool(7*24*60*60, 200); err == nil {
		if a := toolMixStuckAdvisory(tallyRows); a != "" {
			advisories = append(advisories, a)
		}
	}
	// #1303 Phase 2a: branch-drift advisory. For each project whose
	// `current_branch` (stamped at index time) differs from the branch
	// currently checked out on disk, surface a re-index prompt. Catches
	// the silent "I switched branches and forgot to re-index → stale
	// symbol byte-offsets" failure mode. Walks the full project list
	// (not the top-truncated one) so a small-by-symbol-count project
	// on a drifted branch still surfaces.
	if a := branchDriftAdvisory(plist); a != "" {
		advisories = append(advisories, a)
	}
	// #1612 v0.87: FTS5 fragmentation advisory. The per-corpus FTS5
	// vtabs accumulate micro-segments as INSERTs land; BM25 latency
	// degrades long before the user notices a slow `search`. The
	// data/idx-row ratio is the cheapest accurate indicator —
	// healthy at 1–3x, fragmented at 10x+. Best-effort: a missing
	// shadow table (pre-v9 schema or partial migration) returns
	// no rows and the advisory stays silent.
	if fragRows, err := s.store.FTS5Fragmentation(); err == nil {
		if a := fts5FragmentationAdvisory(fragRows); a != "" {
			advisories = append(advisories, a)
		}
	}
	// settings_flood advisory: a project whose symbol surface is
	// dominated by Setting symbols indexed data artifacts (the 27k
	// junk-Setting-symbols shape) — point the user at .pincherignore.
	// One aggregate GROUP BY query; walks the full project list so a
	// flooded project outside the display slice still surfaces.
	if settingCounts, err := s.store.SettingSymbolCountsByProject(); err == nil {
		if a := settingsFloodAdvisory(allProjects, settingCounts); a != "" {
			advisories = append(advisories, a)
		}
	}
	data["advisories"] = advisories

	type failureRow struct {
		Project    string `json:"project"`
		File       string `json:"file"`
		Language   string `json:"language"`
		Reason     string `json:"reason"`
		Details    string `json:"details"`
		LastSeenAt string `json:"last_seen_at"`
		// IsStale fires when last_seen_at predates the project's
		// current indexed_at — the row was recorded in a prior pass
		// but the most-recent re-extraction did NOT re-record it.
		// PruneExtractionFailuresForFile (#1319) handles that case
		// for files the indexer touched, but a project whose binary
		// was upgraded and never re-indexed since still surfaces
		// pre-upgrade rows here. Distinguishes "happening now" from
		// "awaiting re-index to clear" — #1382.
		IsStale bool `json:"is_stale,omitempty"`
		// BinaryVersionAtFailure is the pincher binary version that
		// recorded the row. Lets the reader tell "fixed-since" rows
		// from "still recurring on the running binary" without
		// cross-referencing CHANGELOG by hand — #1421. Empty string
		// when the row pre-dates schema v33 or the binary ran without
		// a version stamp (dev builds, source-built `go build`).
		BinaryVersionAtFailure string `json:"binary_version_at_failure,omitempty"`
	}
	// Pre-build project indexed_at lookup so staleness comparison is O(1)
	// per failure row instead of O(plist).
	indexedAtByID := make(map[string]time.Time, len(plist))
	for _, p := range plist {
		indexedAtByID[p.ID] = p.IndexedAt
	}
	failures := []failureRow{}
	cutoff := time.Now().Add(-time.Duration(lookbackHours) * time.Hour)
	// #1205: pre-fix doctor looped `ListExtractionFailures(p.ID, top)` per
	// project — N round-trips on a 130-project install, each scanning the
	// per-project segment of the `(project_id, last_seen_at DESC)` index.
	// On an 11GB DB with WAL contention the loop alone burned ~60s.
	// Replace with one cross-project SELECT capped at `top`; an honest
	// truncation count comes from a cheap second COUNT against the same
	// cutoff. Project-name join happens in-memory from `plist` (already
	// fetched above).
	projectName := make(map[string]string, len(plist))
	for _, p := range plist {
		projectName[p.ID] = p.Name
	}
	cutoffUnix := cutoff.Unix()
	recentFails, err := s.store.ListRecentExtractionFailuresAcrossProjects(cutoffUnix, top)
	if err == nil {
		for _, f := range recentFails {
			// #1401: respect the project filter on the failures section
			// too. Filtering happens after the SQL fetch (rather than
			// pushing a WHERE clause down to ListRecentExtractionFailures
			// AcrossProjects) so the existing reader-pool path stays
			// unchanged — the cost is one map lookup per row, bounded
			// to `top` rows.
			if matchedProjectIDs != nil {
				if _, ok := matchedProjectIDs[f.ProjectID]; !ok {
					continue
				}
			}
			failures = append(failures, failureRow{
				Project:                projectName[f.ProjectID],
				File:                   f.FilePath,
				Language:               f.Language,
				Reason:                 f.Reason,
				Details:                f.Details,
				LastSeenAt:             f.LastSeenAt.Format(time.RFC3339),
				IsStale:                isStaleFailure(f.LastSeenAt, indexedAtByID[f.ProjectID]),
				BinaryVersionAtFailure: f.BinaryVersionAtFailure,
			})
		}
	}
	failuresTruncated := 0
	if len(failures) >= top {
		if total, cerr := s.store.CountRecentExtractionFailuresAcrossProjects(cutoffUnix); cerr == nil && total > len(failures) {
			failuresTruncated = total - len(failures)
		}
	}
	data["extraction_failures"] = failures
	if failuresTruncated > 0 {
		data["extraction_failures_truncated"] = failuresTruncated
	}

	type slowRow struct {
		Tool       string `json:"tool"`
		Project    string `json:"project,omitempty"`
		DurationMS int64  `json:"duration_ms"`
		Arguments  string `json:"arguments"`
		OccurredAt string `json:"occurred_at"`
	}
	slow := []slowRow{}
	if rows, err := s.store.ListSlowQueries(top * 5); err == nil {
		for _, sq := range rows {
			if sq.OccurredAt.Before(cutoff) {
				continue
			}
			slow = append(slow, slowRow{
				Tool:       sq.Tool,
				Project:    sq.ProjectID,
				DurationMS: sq.DurationMS,
				Arguments:  sq.Arguments,
				OccurredAt: sq.OccurredAt.Format(time.RFC3339),
			})
			if len(slow) >= top {
				break
			}
		}
	}
	data["slow_queries"] = slow

	return s.jsonResultWithMeta(data, start, tool, args, 0), nil
}

// isStaleFailure reports whether an extraction_failures row was recorded
// in a pre-current-indexed-at pass and is awaiting a fresh re-extraction
// to either re-record or implicitly clear (via PruneExtractionFailuresForFile,
// #1319). Cleared-but-not-re-indexed projects accumulate rows whose
// last_seen_at predates the project's most-recent indexed_at — these are
// what we surface with the IsStale tag in doctor output (#1382). When
// indexedAt is the zero time (project record missing the column for some
// reason), conservatively return false so we don't false-positive.
//
// Mirrors `cmd/pinch/project.go::isStaleFailure` per the
// bounded-duplication convention; the two must stay byte-identical.
func isStaleFailure(failureLastSeen, indexedAt time.Time) bool {
	if indexedAt.IsZero() {
		return false
	}
	return failureLastSeen.Before(indexedAt)
}

// doctorMatchedProjectIDsForFilter implements the MCP doctor tool's
// `project` arg with the same tiered match `pincher project rm` uses
// (#1404 fix). Pre-fix the flat substring check over-matched on
// nested project IDs: filter "pincher-repo" returned every corpus
// sub-project whose id `d:\claudecode\pincher-repo\testdata\corpus\*`
// contained the parent path as a substring.
//
// Resolution order:
//  1. Exact id match (case-sensitive — IDs are stable).
//  2. Exact (case-insensitive) name match. >1 hit is legitimate
//     (name collision); we include all of them.
//  3. Substring on name OR id — fallback when no exact tier hit.
//
// Returns nil when filter == "" (no filter; caller treats nil as
// "include every project"). Returns a non-nil empty map when filter
// is set but nothing matched (filter applied, all excluded).
//
// Mirrors `cmd/pinch/doctor.go::matchedProjectIDsForFilter` per the
// bounded-duplication convention. The doctor* prefix exists because
// admin.go can't import cmd/pinch and we don't yet have a shared
// pkg/projectmatch util.
func doctorMatchedProjectIDsForFilter(ps []db.Project, filter string) map[string]struct{} {
	if filter == "" {
		return nil
	}
	hits := make(map[string]struct{})
	for _, p := range ps {
		if p.ID == filter {
			hits[p.ID] = struct{}{}
			return hits
		}
	}
	for _, p := range ps {
		if strings.EqualFold(p.Name, filter) {
			hits[p.ID] = struct{}{}
		}
	}
	if len(hits) > 0 {
		return hits
	}
	low := doctorToLowerASCII(filter)
	for _, p := range ps {
		if strings.Contains(doctorToLowerASCII(p.Name), low) || strings.Contains(doctorToLowerASCII(p.ID), low) {
			hits[p.ID] = struct{}{}
		}
	}
	return hits
}

func doctorToLowerASCII(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

// handleRebuildFTS rebuilds every FTS5 index from the canonical symbols
// table. Mutating; requires confirm=true to actually run. Without
// confirm, returns the projected work (current FTS row count) so callers
// can preview before triggering.
func (s *Server) handleRebuildFTS(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)
	confirm := boolArg(args, "confirm")

	if !confirm {
		var rows int64
		_ = s.store.DB().QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&rows)
		return s.jsonResultWithMeta(map[string]any{
			"dry_run":               true,
			"would_reindex_symbols": rows,
			"hint":                  "pass confirm=true to perform the rebuild",
		}, start, tool, args, 0), nil
	}

	t0 := time.Now()
	// #1950: stage-level progress (drops, schema recreate, 3 per-corpus
	// backfills, row count) when the client supplied a progressToken.
	// Opt-in; no-op without a token / live session (see runWithProgress).
	var stagesDone atomic.Int64
	var rows int64
	if err := s.runWithProgress(ctx, req,
		func() (int64, int64, bool) { return stagesDone.Load(), db.RebuildFTSStages, true },
		"rebuild_fts: stage",
		func() error {
			var rerr error
			rows, rerr = s.store.RebuildFTSWithProgress(func(done, _ int64) { stagesDone.Store(done) })
			return rerr
		},
	); err != nil {
		return errResult(fmt.Sprintf("rebuild_fts: %v", err)), nil
	}
	return s.jsonResultWithMeta(map[string]any{
		"dry_run":      false,
		"rebuilt_rows": rows,
		"duration_ms":  time.Since(t0).Milliseconds(),
	}, start, tool, args, 0), nil
}

// handleSelfTest exercises the open-DB → index → search → byte-offset
// retrieval loop against a synthetic Go project in a temp directory.
// Returns per-step pass/fail. The temp project is cleaned up before
// return; the caller's primary DB is untouched (we open a fresh DB in
// the temp dir so we don't pollute project counts on healthy installs).
func (s *Server) handleSelfTest(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start, tool, args := beginCall(req)

	type stepResult struct {
		Label      string `json:"label"`
		OK         bool   `json:"ok"`
		DurationMS int64  `json:"duration_ms"`
		Error      string `json:"error,omitempty"`
	}
	steps := []stepResult{}
	allOK := true
	addStep := func(label string, dur time.Duration, err error) bool {
		ok := err == nil
		errMsg := ""
		if !ok {
			errMsg = err.Error()
			allOK = false
		}
		steps = append(steps, stepResult{
			Label:      label,
			OK:         ok,
			DurationMS: dur.Milliseconds(),
			Error:      errMsg,
		})
		return ok
	}

	tmp, err := os.MkdirTemp("", "pincher-selftest-*")
	if err != nil {
		return errResult(fmt.Sprintf("self_test: tmp dir: %v", err)), nil
	}
	defer os.RemoveAll(tmp)

	dataDir := filepath.Join(tmp, "data")
	projDir := filepath.Join(tmp, "project")

	// Step 1: open a fresh DB in tmp.
	t0 := time.Now()
	if err := os.MkdirAll(dataDir, 0o755); err == nil {
		_, err = db.Open(dataDir)
		if err == nil {
			// Reopen handle for subsequent steps.
		}
	}
	store, openErr := db.Open(dataDir)
	if !addStep("1/5 open database", time.Since(t0), openErr) {
		return s.jsonResultWithMeta(map[string]any{"ok": allOK, "steps": steps}, start, tool, args, 0), nil
	}
	defer store.Close()
	idx := index.New(store)

	// Step 2: synth project.
	t0 = time.Now()
	src := []byte(`package selftest

func SelfTestProbe(x int) int { return x + 1 }
`)
	if err := os.MkdirAll(projDir, 0o755); err == nil {
		err = os.WriteFile(filepath.Join(projDir, "main.go"), src, 0o644)
		if !addStep("2/5 create synthetic project", time.Since(t0), err) {
			return s.jsonResultWithMeta(map[string]any{"ok": allOK, "steps": steps}, start, tool, args, 0), nil
		}
	}

	// Step 3: index it.
	t0 = time.Now()
	res, err := idx.Index(ctx, projDir, false)
	if err == nil && res.Symbols == 0 {
		err = fmt.Errorf("indexer reported 0 symbols on a project with one Go function")
	}
	if !addStep("3/5 index the project", time.Since(t0), err) {
		return s.jsonResultWithMeta(map[string]any{"ok": allOK, "steps": steps}, start, tool, args, 0), nil
	}
	pid := db.ProjectIDFromPath(projDir)

	// Step 4: search.
	t0 = time.Now()
	results, err := store.SearchSymbolsByCorpus(pid, "SelfTestProbe", "", "", "code", 5)
	if err == nil && len(results) == 0 {
		err = fmt.Errorf("search for SelfTestProbe returned 0 results — FTS5 trigger may not be firing")
	}
	if !addStep("4/5 search for known symbol", time.Since(t0), err) {
		return s.jsonResultWithMeta(map[string]any{"ok": allOK, "steps": steps}, start, tool, args, 0), nil
	}
	symID := results[0].Symbol.ID

	// Step 5: byte-offset retrieval.
	t0 = time.Now()
	sym, err := store.GetSymbol(symID)
	if err == nil && sym == nil {
		err = fmt.Errorf("GetSymbol returned nil for ID %q surfaced by search", symID)
	}
	if err == nil {
		var srcOut string
		srcOut, err = index.ReadSymbolSource(projDir, *sym)
		if err == nil && srcOut == "" {
			err = fmt.Errorf("byte-offset retrieval returned empty string for non-empty symbol")
		}
	}
	addStep("5/5 retrieve symbol source via byte offsets", time.Since(t0), err)

	return s.jsonResultWithMeta(map[string]any{
		"ok":    allOK,
		"steps": steps,
	}, start, tool, args, 0), nil
}

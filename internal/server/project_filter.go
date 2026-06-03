// SPDX-License-Identifier: MIT

package server

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/kwad77/pincher/internal/db"
)

// projectFilterOpts mirrors the MCP `list` tool's filter set so the
// HTTP /v1/projects endpoint can offer the same orientation cleanup
// (#707). Read-only — prune_dead lives in the MCP path and is not
// exposed via HTTP by design.
type projectFilterOpts struct {
	ActiveOnly       bool
	ActiveWithinDays int
	IncludeDead      bool
	MinEdges         int
}

// projectFilterDrops counts per-reason drops so callers can report
// which knob would recover a missing entry. Same buckets the MCP
// `list` response uses.
type projectFilterDrops struct {
	DeadPath int
	Inactive int
	LowEdges int
}

// filterProjects applies the orientation filters from projectFilterOpts
// to a raw project slice. Returns the filtered set + per-reason drop
// counts. Single source of truth for the "drop dead-path / drop
// inactive / drop low-edges" logic used by both the MCP list tool
// and the HTTP /v1/projects endpoint (#707).
func filterProjects(projects []db.Project, opts projectFilterOpts) ([]db.Project, projectFilterDrops) {
	cutoff := time.Now().Add(-time.Duration(opts.ActiveWithinDays) * 24 * time.Hour)
	var out []db.Project
	var drops projectFilterDrops
	for _, p := range projects {
		_, statErr := os.Stat(p.Path)
		dead := os.IsNotExist(statErr)
		if dead && !opts.IncludeDead {
			drops.DeadPath++
			continue
		}
		if opts.ActiveOnly && p.IndexedAt.Before(cutoff) {
			drops.Inactive++
			continue
		}
		if opts.MinEdges > 0 && p.EdgeCount < opts.MinEdges {
			drops.LowEdges++
			continue
		}
		out = append(out, p)
	}
	return out, drops
}

// projectFilterOptsFromQuery reads the HTTP query string for the same
// flag set the MCP `list` tool accepts (#707).
//
// Defaults are intentionally unfiltered: dashboard consumers
// (loadProjects, populateSearchProjects, ADR dropdown) rely on
// /v1/projects returning every row regardless of activity / edges /
// dead-path. Changing defaults would silently drop entries from those
// dropdowns, so the filters are OPT-IN. Pass any of the four flags to
// engage the MCP-style filtering — e.g. ?active=true&min_edges=1 to
// mirror the MCP list orientation view.
//
// When NO flags are set: returns "filter-off" opts (no rows dropped).
func projectFilterOptsFromQuery(r *http.Request) projectFilterOpts {
	opts := projectFilterOpts{
		ActiveOnly:       false,
		ActiveWithinDays: 14, // window default; only used when ActiveOnly=true
		IncludeDead:      true,
		MinEdges:         0,
	}
	q := r.URL.Query()
	if v := q.Get("active"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			opts.ActiveOnly = b
		}
	}
	if v := q.Get("active_within_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.ActiveWithinDays = n
		}
	}
	if v := q.Get("include_dead"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			opts.IncludeDead = b
		}
	}
	if v := q.Get("min_edges"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			opts.MinEdges = n
		}
	}
	return opts
}

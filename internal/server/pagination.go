// SPDX-License-Identifier: MIT

package server

import (
	"net/http"
	"strconv"
)

// pageParams parses ?limit=&offset= query parameters with bounds.
// Used by /v1/projects (#530) and /v1/sessions (#531) to lift the
// previously-hardcoded result counts into a client-controlled knob.
//
// Defaults and caps differ per endpoint — sessions can ask for more
// rows than projects because each row is smaller and the sparkline
// chart benefits from a longer history. Endpoints pass their own
// `defaultLimit` and `maxLimit`. Negative or non-numeric inputs fall
// back to the default; values above maxLimit are clamped (not
// rejected) so a client passing limit=99999 still gets a useful
// response, just bounded.
type pageParams struct {
	Limit  int
	Offset int
}

func parsePageParams(r *http.Request, defaultLimit, maxLimit int) pageParams {
	p := pageParams{Limit: defaultLimit, Offset: 0}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.Limit = n
		}
	}
	if p.Limit > maxLimit {
		p.Limit = maxLimit
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.Offset = n
		}
	}
	return p
}

// sliceWindow extracts a window from a slice given Limit + Offset,
// returning the page slice plus the total count (pre-windowing) and
// whether there are more rows after this window. When offset is
// past the end of the slice, returns an empty page (not nil) so the
// JSON encodes as `[]` not `null` (#334-class invariant).
func sliceWindow[T any](rows []T, p pageParams) (page []T, total int, hasMore bool) {
	total = len(rows)
	if p.Offset >= total {
		return []T{}, total, false
	}
	end := p.Offset + p.Limit
	if end > total {
		end = total
	}
	page = rows[p.Offset:end]
	hasMore = end < total
	return page, total, hasMore
}

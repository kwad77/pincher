// SPDX-License-Identifier: MIT

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// handleEvents serves GET /v1/events as a Server-Sent Events stream
// (#654). It emits three event types:
//
//   - index_started  — a re-index began (project_id, path, started_at,
//     file_count_estimate)
//   - index_complete — a re-index finished (project_id, files, symbols,
//     edges, duration_ms, …)
//   - binary_drift   — a project's index was produced by a binary other
//     than the one now running (project_id, running_version,
//     indexed_version)
//
// Connection-scoped, no persistent subscription state. An optional
// `?project=<id>` query filters the stream to one project. On connect
// the handler sends a binary_drift snapshot for every currently-drifted
// project, then streams live events until the client disconnects.
//
// Routed before the gzip wrap in ServeHTTP: gzipResponseWriter buffers
// and isn't an http.Flusher, which would strand every SSE frame — the
// same hazard #687 fixed for the streamable-HTTP transport.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported",
			"SSE requires a flushable ResponseWriter")
		return
	}

	projectFilter := r.URL.Query().Get("project")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	w.WriteHeader(http.StatusOK)

	ch, unsub := s.events.subscribe()
	defer unsub()

	// Initial snapshot: a freshly-connected client needs to know which
	// projects are already drifted — those events fired before it
	// subscribed. Subscribe-then-snapshot (not the reverse) so a drift
	// that arises in the gap is delivered live, not missed.
	for _, ev := range s.driftSnapshot() {
		if projectFilter != "" && ev.ProjectID != projectFilter {
			continue
		}
		writeSSE(w, flusher, ev)
	}
	flusher.Flush()

	ctx := r.Context()
	// Keepalive comment frames stop intermediary proxies from culling an
	// idle stream. SSE comments (": …") are ignored by EventSource.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			if projectFilter != "" && ev.ProjectID != projectFilter {
				continue
			}
			writeSSE(w, flusher, ev)
		}
	}
}

// writeSSE marshals one event as an SSE frame and flushes it. The
// payload's `type` key is set from ev.Type so a consumer that only
// reads the data line still gets the discriminator.
//
// The event bus fans the SAME sseEvent (and the same Payload map) out
// to every subscriber, so writeSSE must NOT mutate ev.Payload — two
// subscriber goroutines writing `type` into the shared map while a
// third marshals it is a "concurrent map write" fatal. It builds a
// shallow copy instead; the copy is goroutine-local.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, ev sseEvent) {
	out := make(map[string]any, len(ev.Payload)+1)
	for k, v := range ev.Payload {
		out[k] = v
	}
	out["type"] = ev.Type
	data, err := json.Marshal(out)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
	flusher.Flush()
}

// driftSnapshot returns a binary_drift event for every indexed project
// whose stored binary_version differs from the running server's
// version — i.e. projects the current binary hasn't re-indexed yet.
// Best-effort: a store error yields an empty snapshot rather than
// failing the stream.
func (s *Server) driftSnapshot() []sseEvent {
	projects, err := s.store.ListProjects()
	if err != nil {
		return nil
	}
	out := make([]sseEvent, 0)
	for _, p := range projects {
		if p.BinaryVersion != "" && p.BinaryVersion != s.version {
			out = append(out, sseEvent{
				Type:      "binary_drift",
				ProjectID: p.ID,
				Payload: map[string]any{
					"project_id":      p.ID,
					"project":         p.Name,
					"running_version": s.version,
					"indexed_version": p.BinaryVersion,
				},
			})
		}
	}
	return out
}

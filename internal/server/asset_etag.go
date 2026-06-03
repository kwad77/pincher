// SPDX-License-Identifier: MIT

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// writeAssetWithETagAndGzip serves a static asset body with #556's
// ETag short-circuit. The "AndGzip" in the name is historical — the
// dispatcher (server.ServeHTTP) already applies a transparent gzip
// middleware to every response when the client sent
// Accept-Encoding: gzip, so this helper would double-gzip if it
// added its own pass. The middleware also sets Content-Encoding +
// Vary correctly. We only handle ETag here.
//
// ETag: sha256(body)[:8] hex-encoded. If the request's If-None-Match
// matches, returns 304 and writes no body — the browser already has
// the asset and saves the body bytes (and the bytes the outer gzip
// middleware would have spent compressing them).
//
// Caller must have already set Content-Type, Cache-Control, and any
// security headers BEFORE calling this.
func writeAssetWithETagAndGzip(w http.ResponseWriter, r *http.Request, body []byte) {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	// Vary: Accept-Encoding so caches don't serve a gzipped response
	// to a non-gzip client. Belt-and-suspenders — the outer middleware
	// also sets Content-Encoding which implies Vary, but explicit is
	// safer for shared caches.
	w.Header().Set("Vary", "Accept-Encoding")

	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

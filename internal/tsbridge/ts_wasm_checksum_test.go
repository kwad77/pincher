// SPDX-License-Identifier: MIT

package tsbridge

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"strings"
	"testing"
)

// ts.wasm.sha256 pins the SHA-256 of the embedded WASM blob (v1.4.0
// release-review hardening, adversarial finding #3 / ADR-0008 §risks).
//
// ts.wasm is a 20+ MB opaque binary that parses untrusted repository
// content and emits confidence-1.0 symbols and edges. Until the
// reproducible-build CI pipeline ADR-0008 commits to actually ships
// (rebuild with pinned zig + grammar versions → identical checksum),
// this pin is the interim supply-chain gate: the blob can no longer be
// swapped silently inside an unrelated diff. Any intentional rebuild
// must update BOTH files in the same commit, which makes the change
// visible and reviewable on its own.
//
//go:embed ts.wasm.sha256
var tsWasmPinnedSHA256 string

func TestTSWasm_MatchesPinnedChecksum(t *testing.T) {
	want := strings.TrimSpace(tsWasmPinnedSHA256)
	if len(want) != 64 {
		t.Fatalf("ts.wasm.sha256 must contain exactly one 64-hex-char SHA-256, got %q", want)
	}
	sum := sha256.Sum256(tsWasm)
	got := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("embedded ts.wasm SHA-256 = %s, pinned = %s\n"+
			"If the WASM rebuild is intentional, update internal/tsbridge/ts.wasm.sha256 in the SAME commit\n"+
			"and document the grammar/zig versions used for the rebuild in the commit message (ADR-0008).",
			got, want)
	}
}

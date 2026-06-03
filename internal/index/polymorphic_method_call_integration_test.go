// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #465: integration test — a corpus that defines a single Method named
// String, paired with callers that invoke `.String()` on stdlib values
// (time.Time, bytes.Buffer, *url.URL), must NOT attribute those calls
// to the project-local Method. Pre-fix, the receiver-method fallback
// (#285) bound every `.String()` to the only project Method named
// String, polluting trace + architecture hotspots.

const polymorphicStringSrc = `package svc

import (
	"bytes"
	"net/url"
	"time"
)

// The one project-local Method named String. Pre-fix #465, every
// dot-String call in the project bound here.
type bytesCollector struct {
	buf bytes.Buffer
}

func (b *bytesCollector) String() string {
	return b.buf.String()
}

// These functions call dot-String on stdlib values. Post-fix, none
// should produce a CALLS edge to bytesCollector.String.

func formatTimestamp(ts time.Time) string {
	return ts.String()
}

func formatURL(u *url.URL) string {
	return u.String()
}

func formatBuffer(b *bytes.Buffer) string {
	return b.String()
}

// Sanity: a function that DOES call our local String() via the
// concrete type. With type-aware resolution this would resolve; the
// blocklist drops it too — under-counting is the documented tradeoff.
func runCollector(c *bytesCollector) string {
	return c.String()
}
`

func TestResolveCalls_PolymorphicStringMethodNotResolved(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "svc/svc.go", polymorphicStringSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	syms, _ := store.GetSymbolsByName(pid, "String", 5)
	var stringID string
	for _, s := range syms {
		if s.Kind == "Method" {
			stringID = s.ID
			break
		}
	}
	if stringID == "" {
		t.Skip("String not extracted as Method on this corpus; nothing to assert")
	}

	results, err := store.TraceViaCTEScoped(pid, stringID, "inbound", []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("TraceViaCTEScoped: %v", err)
	}

	// Pre-fix: 4 callers (formatTimestamp, formatURL, formatBuffer,
	// runCollector). Post-fix: 0 — the blocklist drops every
	// receiver-method fallback for String, including the genuine
	// runCollector case. That's the documented under-counting tradeoff.
	for _, r := range results {
		sym, err := store.GetSymbol(r.SymbolID)
		if err != nil || sym == nil {
			continue
		}
		switch sym.Name {
		case "formatTimestamp", "formatURL", "formatBuffer":
			t.Errorf("polymorphic-method blocklist failed: %q falsely resolved as caller of *bytesCollector.String", sym.Name)
		}
	}
}

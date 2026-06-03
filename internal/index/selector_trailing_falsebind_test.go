// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #758: resolveByReceiverType's case-2 path builds the target method QN
// as `pkg.<enclosingReceiverType>.<trailing>` — using the receiver type
// of the *enclosing* method, never verifying that segments[0] is the
// receiver variable. So a package-function call like `strings.Index(...)`
// inside `func (b *box) scan()` built methodQN `box.*box.Index` and
// false-bound to the unrelated `*box.Index` method. The fix skips case
// 2/3 when segments[0] is a Go stdlib package (same stoplist the #410
// receiver-method fallback already uses).
func TestIndex_SelectorTrailing_NoFalseBindToEnclosingReceiverMethod(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	// `*box` has a method `Index` AND a method `scan` whose body calls
	// `strings.Index(...)`. Pre-#758 `scan` false-bound to `box.Index`
	// because the enclosing receiver type `*box` + trailing `Index`
	// rebuilt the real method's QN.
	writeFile(t, dir, "box/box.go",
		"package box\n\nimport \"strings\"\n\ntype box struct{}\n\n"+
			"func (b *box) Index() {}\n\n"+
			"func (b *box) scan(s string) {\n\t_ = strings.Index(s, \"x\")\n}\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	scanID := db.MakeSymbolID("box/box.go", "box.*box.scan", "Method")
	indexMethodID := db.MakeSymbolID("box/box.go", "box.*box.Index", "Method")

	inbound, err := store.EdgesTo(indexMethodID, nil)
	if err != nil {
		t.Fatalf("EdgesTo box.*box.Index: %v", err)
	}
	if hasEdge(inbound, scanID, "CALLS") {
		t.Errorf("strings.Index(...) inside *box.scan false-bound to the *box.Index method:\n  inbound: %v", inbound)
	}
}

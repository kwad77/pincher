// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #1177 v0.72: TS receiver-type resolver. Mirrors #423 piece 3
// (`receiver_type_resolver_test.go`) but for TypeScript. The
// extractor stamps ReceiverType on CALLS edges for `this.X()` and
// `varname.X()` shapes; the resolver binds them precisely to
// `Class.method` via a class-name lookup (TS module paths are
// multi-segment so the Go-flavoured pkg-prefix construction can't
// reach the real Class.method QN).
//
// Headline acceptance from #1177:
//   Two classes both defining `add()` — `(c: Cart).add()` must
//   resolve to Cart.add only, NOT Wishlist.add.

const tsTwoClassesAddSrc = `export class Cart {
	add(item: Item): void {}
}

export class Wishlist {
	add(item: Item): void {}
}

export function handle(c: Cart): void {
	c.add(1);
}
`

// TestResolveCalls_TS_PolymorphicMethodBindsByReceiverType is the
// headline guard: parameter `c: Cart` typing binds c.add() to
// Cart.add and NOT to Wishlist.add. Pre-#1177 the resolver fell back
// to a name-only Method lookup that picked one of the two arbitrarily
// (or dropped both via the polymorphic blocklist).
func TestResolveCalls_TS_PolymorphicMethodBindsByReceiverType(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/cart.ts", tsTwoClassesAddSrc)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	syms, err := store.GetSymbolsByName(pid, "add", 10)
	if err != nil {
		t.Fatalf("GetSymbolsByName add: %v", err)
	}
	var cartAdd, wishlistAdd string
	for _, s := range syms {
		if s.Kind != "Method" {
			continue
		}
		if s.Parent == "src.cart.Cart" {
			cartAdd = s.ID
		}
		if s.Parent == "src.cart.Wishlist" {
			wishlistAdd = s.ID
		}
	}
	if cartAdd == "" || wishlistAdd == "" {
		t.Fatalf("expected both add Methods extracted; cart=%q wishlist=%q (parents seen: %s)",
			cartAdd, wishlistAdd, parentsOf(syms))
	}

	// Cart.add must have `handle` as caller.
	results, _ := store.TraceViaCTEScoped(pid, cartAdd, "inbound", []string{"CALLS"}, 3)
	handleCount := 0
	for _, r := range results {
		sym, _ := store.GetSymbol(r.SymbolID)
		if sym != nil && sym.Name == "handle" {
			handleCount++
		}
	}
	if handleCount != 1 {
		t.Errorf("Cart.add inbound: got %d handle callers, want 1 (#1177)", handleCount)
	}

	// Wishlist.add must NOT have `handle` as caller — that would be
	// the pre-#1177 false bind from name-only resolution.
	results, _ = store.TraceViaCTEScoped(pid, wishlistAdd, "inbound", []string{"CALLS"}, 3)
	for _, r := range results {
		sym, _ := store.GetSymbol(r.SymbolID)
		if sym != nil && sym.Name == "handle" {
			t.Errorf("Wishlist.add unexpectedly has handle as caller — false bind across types (pre-#1177 shape)")
		}
	}
}

// TestResolveCalls_TS_ThisMethodBindsToEnclosingClass — the `this.X()`
// flavour of the same fix. Two classes both have `step()`; calling
// `this.step()` inside Cart.process must bind to Cart.step, not
// Wishlist.step. Pre-#1177 the bare-name fallback could pick either.
func TestResolveCalls_TS_ThisMethodBindsToEnclosingClass(t *testing.T) {
	src := `export class Cart {
	process(): void {
		this.step();
	}
	step(): void {}
}

export class Wishlist {
	step(): void {}
}
`
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/cart.ts", src)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}

	pid := db.ProjectIDFromPath(dir)
	syms, _ := store.GetSymbolsByName(pid, "step", 10)
	var cartStep, wishlistStep string
	for _, s := range syms {
		if s.Kind != "Method" {
			continue
		}
		if s.Parent == "src.cart.Cart" {
			cartStep = s.ID
		}
		if s.Parent == "src.cart.Wishlist" {
			wishlistStep = s.ID
		}
	}
	if cartStep == "" || wishlistStep == "" {
		t.Fatalf("expected both step Methods extracted; cart=%q wishlist=%q",
			cartStep, wishlistStep)
	}

	// Cart.step must have process as caller (this.step() inside
	// Cart.process binds to Cart.step).
	results, _ := store.TraceViaCTEScoped(pid, cartStep, "inbound", []string{"CALLS"}, 3)
	processCount := 0
	for _, r := range results {
		sym, _ := store.GetSymbol(r.SymbolID)
		if sym != nil && sym.Name == "process" {
			processCount++
		}
	}
	if processCount != 1 {
		t.Errorf("Cart.step inbound: got %d process callers, want 1", processCount)
	}

	// Wishlist.step must NOT have process as caller.
	results, _ = store.TraceViaCTEScoped(pid, wishlistStep, "inbound", []string{"CALLS"}, 3)
	for _, r := range results {
		sym, _ := store.GetSymbol(r.SymbolID)
		if sym != nil && sym.Name == "process" {
			t.Errorf("Wishlist.step unexpectedly has process as caller — false bind across types")
		}
	}
}

// parentsOf is a debug helper for the failure path — surfaces every
// Parent value seen during a failed lookup so the test message
// pinpoints whether extraction parented the symbols differently than
// the test expected.
func parentsOf(syms []db.Symbol) string {
	out := "["
	for i, s := range syms {
		if i > 0 {
			out += ", "
		}
		out += s.Parent
	}
	return out + "]"
}

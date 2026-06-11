// SPDX-License-Identifier: MIT
// Vendored and adapted from github.com/malivvan/tree-sitter (MIT) — pincher in-tree binding.

package tsbridge

import (
	"context"
	"fmt"
)

type Tree struct {
	ts TreeSitter
	t  uint64
}

func newTree(ts TreeSitter, t uint64) Tree {
	return Tree{ts, t}
}

// Close frees the underlying tree-sitter parse tree (ts_tree_delete). It must
// be called once per Tree when reusing a parser/instance across many files,
// or the WASM heap grows unbounded. The Node structs handed out by this tree
// are separately owned by the binding (24-byte scratch allocations) and must
// be freed via Node.Free.
func (t Tree) Close(ctx context.Context) error {
	_, err := t.ts.treeDelete.Call(ctx, t.t)
	if err != nil {
		return fmt.Errorf("deleting tree: %w", err)
	}
	return nil
}

func (t Tree) RootNode(ctx context.Context) (Node, error) {
	// allocate tsnode 24 bytes
	nodePtr, err := t.ts.malloc.Call(ctx, uint64(24))
	if err != nil {
		return Node{}, fmt.Errorf("allocating node: %w", err)
	}

	_, err = t.ts.treeRootNode.Call(ctx, nodePtr[0], t.t)
	if err != nil {
		return Node{}, fmt.Errorf("getting tree root node: %w", err)
	}
	return newNode(t.ts, nodePtr[0]), nil
}

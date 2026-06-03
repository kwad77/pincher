// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #304: SetBinaryVersion stamps the version on the project record at
// index time so health can detect drift later. Without this stamp,
// every project looks pre-v18 and the drift detector can't tell
// "indexed by current binary" from "indexed by old binary".

func TestIndex_SetBinaryVersion_StampsOnProject(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	idx := New(store)
	idx.SetBinaryVersion("0.9.0")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	p, err := store.GetProject(result.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if p.BinaryVersion != "0.9.0" {
		t.Errorf("project.BinaryVersion = %q, want 0.9.0", p.BinaryVersion)
	}
}

// Without SetBinaryVersion, the stamp is empty (legitimate "I don't
// know" rather than spoofed). The migration default is also "" so
// pre-v18 rows are indistinguishable from "ran without
// SetBinaryVersion plumbed" — health treats both the same.
func TestIndex_NoBinaryVersion_StampsEmpty(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	idx := New(store)
	// Note: SetBinaryVersion not called.

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	p, err := store.GetProject(result.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if p.BinaryVersion != "" {
		t.Errorf("project.BinaryVersion = %q, want empty", p.BinaryVersion)
	}
}

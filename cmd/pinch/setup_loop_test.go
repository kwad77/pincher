// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	pinit "github.com/kwad77/pincher/internal/init"
)

// setup_loop_test.go — drives the full `pincher setup` wizard loop with
// a scripted key sequence (no TTY), proving the hosts → confirm → apply
// → done flow writes the expected files. The pure transitions are
// covered in setup_model_test.go; this exercises runSetupLoop's
// effect-bearing path. #1710 v0.92.

// scriptedKeys returns a nextKey func that replays ks, then yields
// keyQuit forever so a logic bug can't hang the test.
func scriptedKeys(ks []wkey) func() wkey {
	i := 0
	return func() wkey {
		if i >= len(ks) {
			return keyQuit
		}
		k := ks[i]
		i++
		return k
	}
}

func TestSetupLoop_AppliesSelectedTarget(t *testing.T) {
	setupOut = io.Discard
	defer func() { setupOut = os.Stdout }()

	dir := t.TempDir()
	// Cursor pre-detected → pre-selected. Non-git, cursor-only → no
	// options screen, so the flow is hosts → confirm → done.
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.CursorTarget}, false)

	// enter: hosts → confirm. enter: confirm → apply → done. enter: finish.
	runSetupLoop(&m, dir, scriptedKeys([]wkey{keyEnter, keyEnter, keyEnter}))

	if m.quit {
		t.Fatal("normal completion must not set quit")
	}
	if len(m.results) != 1 || !m.results[0].ok || m.results[0].name != "cursor" {
		t.Fatalf("expected one ok cursor result; got %+v", m.results)
	}
	// CursorTarget writes .cursor/rules/pincher.mdc under cwd.
	if _, err := os.Stat(filepath.Join(dir, ".cursor", "rules", "pincher.mdc")); err != nil {
		t.Errorf("cursor rules file was not written: %v", err)
	}
	if m.indexAfter {
		t.Error("indexAfter should be false when the user finishes with Enter")
	}
}

func TestSetupLoop_QuitBeforeApplyWritesNothing(t *testing.T) {
	setupOut = io.Discard
	defer func() { setupOut = os.Stdout }()

	dir := t.TempDir()
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.CursorTarget}, false)

	// q on the very first (hosts) screen — cancel.
	runSetupLoop(&m, dir, scriptedKeys([]wkey{keyQuit}))

	if !m.quit {
		t.Error("quit on the hosts screen must set m.quit")
	}
	if len(m.results) != 0 {
		t.Errorf("a cancelled wizard must apply nothing; got %+v", m.results)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cursor")); !os.IsNotExist(err) {
		t.Error("a cancelled wizard must not write any files")
	}
}

func TestSetupLoop_IndexAfterFlag(t *testing.T) {
	setupOut = io.Discard
	defer func() { setupOut = os.Stdout }()

	dir := t.TempDir()
	m := newSetupModel(pinit.AllTargets, []pinit.Target{pinit.CursorTarget}, false)

	// hosts → confirm → apply → done, then 'i' on the done screen.
	runSetupLoop(&m, dir, scriptedKeys([]wkey{keyEnter, keyEnter, keyIndex}))

	if !m.indexAfter {
		t.Error("pressing 'i' on the done screen must set indexAfter")
	}
	if len(m.results) != 1 || !m.results[0].ok {
		t.Fatalf("the target should still have been applied; got %+v", m.results)
	}
}

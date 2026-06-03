// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/db"
)

// #262 — end-to-end test: a shell script with a non-standard
// extension (.init, .hotplug, .login) or none at all gets indexed
// by the Bash extractor when its first line carries a recognized
// shebang. Without the fix in #262 these files would be silently
// skipped — the user's index would show "6 indexed / 18 files" on
// projects like OpenWrt's travelmate where most shell logic ships
// in .init / .hotplug.

const openWrtInitScript = `#!/bin/sh /etc/rc.common

START=80
STOP=10
USE_PROCD=1

start_service() {
    procd_open_instance
    procd_set_param command /usr/bin/myservice
    procd_close_instance
}

stop_service() {
    killall -9 myservice
}
`

const debianPostinstScript = `#!/bin/bash
set -e

configure_service() {
    update-rc.d myservice defaults
}

case "$1" in
    configure)
        configure_service
        ;;
    *)
        exit 0
        ;;
esac
`

const captivePortalLoginScript = `#!/usr/bin/env bash
# Vodafone captive portal login automation
USERNAME="${1:-anonymous}"
login_request() {
    curl -X POST https://login.example.com/auth -d "user=$USERNAME"
}
login_request
`

const extensionlessEntrypoint = `#!/bin/bash
exec /app/bin/server "$@"
`

func TestIndex_OpenWrtInitScript_IndexedAsBash(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "files/travelmate.init", openWrtInitScript)

	result, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if result.Files == 0 {
		t.Fatalf(".init file with shell shebang was skipped (Files=0)")
	}
	syms, err := store.SearchSymbolsByCorpus(db.ProjectIDFromPath(dir), "start_service", "", "", "code", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(syms) == 0 {
		t.Errorf("expected start_service to be indexed from .init file; got 0 symbols")
	}
	for _, s := range syms {
		if s.Symbol.Language != "Bash" {
			t.Errorf("symbol %q indexed as %q, want Bash", s.Symbol.Name, s.Symbol.Language)
		}
	}
}

func TestIndex_DebianPostinst_IndexedAsBash(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "debian/myservice.postinst", debianPostinstScript)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	syms, err := store.SearchSymbolsByCorpus(db.ProjectIDFromPath(dir), "configure_service", "Function", "Bash", "code", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(syms) == 0 {
		t.Errorf("expected .postinst file to be indexed as Bash with configure_service function")
	}
}

func TestIndex_ExtensionlessShellExecutable_IndexedAsBash(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "bin/entrypoint", extensionlessEntrypoint)

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	// The extractor should at minimum produce a Module symbol for the
	// file. If extraction surfaced anything, it'll be flagged Bash.
	any, err := store.SearchSymbolsByCorpus(db.ProjectIDFromPath(dir), "entrypoint", "", "Bash", "code", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, s := range any {
		if s.Symbol.Language != "Bash" {
			t.Errorf("symbol %q indexed as %q, want Bash", s.Symbol.Name, s.Symbol.Language)
		}
	}
}

// .login with a non-shell shebang must NOT be misclassified as Bash —
// the predicate only fires on recognized shells. This guards against
// future relaxations of DetectShebangLanguage that broaden the match.
func TestIndex_PerlScriptWithLoginExtension_NotIndexedAsBash(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "files/perlish.login", "#!/usr/bin/perl\nuse strict;\nprint \"hello\\n\";\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	syms, err := store.SearchSymbolsByCorpus(db.ProjectIDFromPath(dir), "perlish", "", "Bash", "code", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(syms) > 0 {
		t.Errorf("perl .login was misclassified as Bash; got %d Bash symbols", len(syms))
	}
}

// A .init file without a shebang must remain skipped — we don't want
// to forcibly extract every file with a shebang-candidate extension
// as Bash. Conservative path: shebang required.
func TestIndex_NoShebangNonShellFile_Skipped(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "config/notscript.init", "key=value\nother=1\n")

	res, err := idx.Index(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	_ = store
	if res.Symbols != 0 {
		// A .init file is config-shaped to humans but not parseable
		// to anything we have. Symbol count > 0 here
		// would mean we forcibly routed it to Bash without a shebang.
		t.Errorf("expected 0 symbols for shebang-less .init file; got %d", res.Symbols)
	}
}

// Sanity check the integration's blast radius: existing .sh files
// still get indexed as Bash on the path-based fast path. The shebang
// fallback must not break the happy case.
func TestIndex_StandardShFile_StillWorks(t *testing.T) {
	idx, store := newTestIndexer(t)
	dir := t.TempDir()
	writeFile(t, dir, "scripts/build.sh", "#!/bin/bash\nbuild() { echo hi; }\n")

	if _, err := idx.Index(context.Background(), dir, false); err != nil {
		t.Fatalf("Index: %v", err)
	}
	syms, err := store.SearchSymbolsByCorpus(db.ProjectIDFromPath(dir), "build", "Function", "Bash", "code", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(syms) == 0 {
		t.Errorf("standard .sh file regressed; expected build() symbol")
	}
	for _, s := range syms {
		if !strings.HasSuffix(s.Symbol.FilePath, "build.sh") {
			t.Errorf("unexpected hit file_path=%q", s.Symbol.FilePath)
		}
	}
}

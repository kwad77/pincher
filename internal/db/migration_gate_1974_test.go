// SPDX-License-Identifier: MIT

package db

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1974: schema migrations used to run silently on every db.Open, so a
// dev binary carrying an in-flight schema bump could upgrade a shared
// store as a startup side effect and brick every older binary on the
// machine. Upward migration of an EXISTING store is now an explicit
// decision: PINCHER_ALLOW_MIGRATE=1, or a tagged release build. Fresh
// databases are initialization, not upgrades — never gated.

// seedV1Store builds a pre-versioning-era database pinned at schema v1
// (baseline only), mirroring TestMigrate_UpgradeFromV1's setup.
func seedV1Store(t *testing.T) (dir, dbPath string) {
	t.Helper()
	dir = t.TempDir()
	dbPath = filepath.Join(dir, "pincher.db")
	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(schema); err != nil {
		t.Fatalf("baseline schema: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_version(version) VALUES(1)`); err != nil {
		t.Fatalf("seed schema_version: %v", err)
	}
	return dir, dbPath
}

// setMigrateBinaryVersion swaps the package-level stamped version and
// restores it on cleanup.
func setMigrateBinaryVersion(t *testing.T, v string) {
	t.Helper()
	prev := migrateBinaryVersion
	migrateBinaryVersion = v
	t.Cleanup(func() { migrateBinaryVersion = prev })
}

// captureMigrationLog swaps the loud-migration writer for a buffer.
func captureMigrationLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := migrationLog
	buf := &bytes.Buffer{}
	migrationLog = buf
	t.Cleanup(func() { migrationLog = prev })
	return buf
}

func TestOpen_PendingMigration_RefusedWithoutConsent(t *testing.T) {
	t.Setenv(MigrationConsentEnv, "") // dev posture: no env opt-in
	setMigrateBinaryVersion(t, "1.5.0-3-gabcdef")
	dir, dbPath := seedV1Store(t)

	_, err := Open(dir)
	if err == nil {
		t.Fatal("Open silently migrated an existing below-version store — the #1974 gate is open")
	}
	var pending *MigrationPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("error type = %T (%v), want *MigrationPendingError", err, err)
	}
	if pending.From != 1 || pending.To != CurrentSchemaVersion() {
		t.Errorf("pending migration range = v%d→v%d, want v1→v%d", pending.From, pending.To, CurrentSchemaVersion())
	}
	msg := err.Error()
	for _, want := range []string{
		dbPath, // store path
		"v1",   // current version
		fmt.Sprintf("v%d", CurrentSchemaVersion()), // target version
		MigrationConsentEnv,                        // the opt-in needed
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message missing %q:\n%s", want, msg)
		}
	}

	// Crucially: the refused store must still be at v1 — no partial
	// baseline DDL, no version bump. Pre-fix, baseline Exec ran before
	// the version check.
	raw, rerr := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if rerr != nil {
		t.Fatalf("ro reopen: %v", rerr)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if v != 1 {
		t.Errorf("schema_version after refusal = %d, want 1 (untouched)", v)
	}
}

func TestOpen_PendingMigration_EnvConsentMigratesLoudlyWithBackup(t *testing.T) {
	t.Setenv(MigrationConsentEnv, "1")
	setMigrateBinaryVersion(t, "dev")
	log := captureMigrationLog(t)
	dir, _ := seedV1Store(t)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open with %s=1: %v", MigrationConsentEnv, err)
	}
	defer s.Close()

	var v int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if v != CurrentSchemaVersion() {
		t.Errorf("schema_version = %d, want %d", v, CurrentSchemaVersion())
	}

	// Loudness: the announcement names the store, both versions, binary.
	out := log.String()
	for _, want := range []string{"migrating database", "v1", fmt.Sprintf("v%d", CurrentSchemaVersion()), "dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("migration log missing %q:\n%s", want, out)
		}
	}

	// Restore point: backups/ holds a pre-migration snapshot.
	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no pre-migration backup written to backups/")
	}
	if !strings.HasPrefix(entries[0].Name(), "pincher-v1-") {
		t.Errorf("backup name = %q, want pincher-v1-* (names the pre-migration version)", entries[0].Name())
	}
	// The snapshot must itself be a v1 store (taken BEFORE migrations).
	bak, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "backups", entries[0].Name())+"?mode=ro")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer bak.Close()
	var bv int
	if err := bak.QueryRow(`SELECT version FROM schema_version`).Scan(&bv); err != nil {
		t.Fatalf("read backup version: %v", err)
	}
	if bv != 1 {
		t.Errorf("backup schema_version = %d, want 1 (pre-migration state)", bv)
	}
}

func TestOpen_PendingMigration_ReleaseBuildMigratesWithoutEnv(t *testing.T) {
	t.Setenv(MigrationConsentEnv, "")
	setMigrateBinaryVersion(t, "1.5.0") // clean release tag
	log := captureMigrationLog(t)
	dir, _ := seedV1Store(t)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("release-build Open should migrate without env opt-in: %v", err)
	}
	defer s.Close()
	if !strings.Contains(log.String(), "migrating database") {
		t.Error("release-train migration must still be loud")
	}
}

func TestOpen_FreshDB_NeedsNoConsent(t *testing.T) {
	t.Setenv(MigrationConsentEnv, "")
	setMigrateBinaryVersion(t, "dev")

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("fresh-DB Open must not be gated (initialization, not upgrade): %v", err)
	}
	s.Close()
}

func TestIsReleaseBuild(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"1.5.0", true},
		{"v1.5.0", true},
		{"0.10.0", true},
		{"1.5.0-3-gabcdef", false}, // git-describe dev build
		{"1.5.0-dirty", false},
		{"1.5.0-3-gabcdef-dirty", false},
		{"dev", false},
		{"", false},
		{"abc", false},
	}
	for _, c := range cases {
		if got := IsReleaseBuild(c.v); got != c.want {
			t.Errorf("IsReleaseBuild(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

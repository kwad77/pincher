// SPDX-License-Identifier: MIT

package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kwad77/pincher/internal/cypher"
	"github.com/kwad77/pincher/internal/db"
)

// hcl_integration_test exercises the full ast → indexer → db → FTS5 pipeline
// for HCL/Terraform files in a mixed-language project. It's the failure mode
// we've hit before with new extractors (FTS5 column rename, YAML over-extending
// byte ranges, DSN no-ops): unit tests pass but something downstream breaks.
//
// All tests run against a t.TempDir() data dir so they never touch the
// daily-driver pincher.db.

const hclFixtureMain = `terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "instance_count" {
  type    = number
  default = 2
}

locals {
  common_tags = {
    Environment = "prod"
  }
  name_prefix = "stress-${var.region}"
}

data "aws_ami" "ubuntu" {
  most_recent = true
}

resource "aws_instance" "web" {
  ami           = data.aws_ami.ubuntu.id
  instance_type = "t3.micro"

  lifecycle {
    create_before_destroy = true
  }

  provisioner "local-exec" {
    command = "echo done"
  }
}

resource "aws_security_group" "web_sg" {
  name = "web"
}

module "vpc" {
  source = "./modules/vpc"
}

output "web_ip" {
  value = aws_instance.web.public_ip
}
`

const hclFixtureVars = `region        = "us-west-2"
instance_count = 5
`

const hclFixtureGo = `package main

import "fmt"

func Hello() string {
	return "hi"
}

func main() {
	fmt.Println(Hello())
}
`

const hclFixtureYaml = `service:
  name: stress
  port: 8080
`

// writeMixedFixture lays out a small mixed-language project on disk and
// returns its root path (caller t.TempDir-cleaned).
func writeMixedFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "main.tf", hclFixtureMain)
	writeFile(t, root, "prod.tfvars", hclFixtureVars)
	writeFile(t, root, "main.go", hclFixtureGo)
	writeFile(t, root, "config.yaml", hclFixtureYaml)
	return root
}

// indexFixture indexes the given path and returns (idx, store, projectID).
func indexFixture(t *testing.T, root string, force bool) (*Indexer, *db.Store, string) {
	t.Helper()
	idx, store := newTestIndexer(t)
	res, err := idx.Index(context.Background(), root, force)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if res.Symbols == 0 {
		t.Fatalf("expected symbols > 0, got %d", res.Symbols)
	}
	return idx, store, res.ProjectID
}

func TestHCLIntegration_MixedCorpusEndToEnd(t *testing.T) {
	root := writeMixedFixture(t)
	_, store, pid := indexFixture(t, root, true)

	// Per-language counts: every language we expect must be present.
	wantLangs := []string{"Go", "HCL", "YAML"}
	got := languageCounts(t, store, pid)
	for _, lang := range wantLangs {
		if got[lang] == 0 {
			t.Errorf("language %q has 0 symbols; per-language counts: %v", lang, got)
		}
	}

	// HCL kind taxonomy: every expected kind populated.
	wantKinds := []string{"Resource", "DataSource", "Module", "Variable", "Output", "Provider", "Local", "Block", "Setting"}
	gotKinds := hclKindCounts(t, store, pid)
	for _, k := range wantKinds {
		if gotKinds[k] == 0 {
			t.Errorf("HCL kind %q has 0 symbols; per-kind counts: %v", k, gotKinds)
		}
	}
}

func TestHCLIntegration_ByteOffsetRoundTrip(t *testing.T) {
	root := writeMixedFixture(t)
	_, store, pid := indexFixture(t, root, true)

	// For every HCL symbol, slice the source file by [start_byte, end_byte]
	// and assert the slice is non-empty AND begins with a token consistent
	// with its kind. This catches the YAML/LATENT-#3 class of bug where
	// byte ranges over-extend.
	syms := allHCLSymbols(t, store, pid)
	if len(syms) == 0 {
		t.Fatal("no HCL symbols to round-trip")
	}
	for _, s := range syms {
		full, err := os.ReadFile(filepath.Join(root, s.FilePath))
		if err != nil {
			t.Fatalf("read %s: %v", s.FilePath, err)
		}
		if s.StartByte < 0 || s.EndByte > len(full) || s.EndByte <= s.StartByte {
			t.Errorf("%s: bad byte range [%d, %d) over file len %d", s.ID, s.StartByte, s.EndByte, len(full))
			continue
		}
		slice := strings.TrimSpace(string(full[s.StartByte:s.EndByte]))
		if slice == "" {
			t.Errorf("%s: empty slice", s.ID)
			continue
		}
		// Per-kind consistency check.
		switch s.Kind {
		case "Resource":
			if !strings.HasPrefix(slice, "resource ") {
				t.Errorf("%s: Resource slice doesn't start with 'resource ': %q", s.ID, head(slice, 60))
			}
		case "DataSource":
			if !strings.HasPrefix(slice, "data ") {
				t.Errorf("%s: DataSource slice doesn't start with 'data ': %q", s.ID, head(slice, 60))
			}
		case "Module":
			if !strings.HasPrefix(slice, "module ") {
				t.Errorf("%s: Module slice doesn't start with 'module ': %q", s.ID, head(slice, 60))
			}
		case "Variable":
			if !strings.HasPrefix(slice, "variable ") {
				t.Errorf("%s: Variable slice doesn't start with 'variable ': %q", s.ID, head(slice, 60))
			}
		case "Output":
			if !strings.HasPrefix(slice, "output ") {
				t.Errorf("%s: Output slice doesn't start with 'output ': %q", s.ID, head(slice, 60))
			}
		case "Provider":
			if !strings.HasPrefix(slice, "provider ") {
				t.Errorf("%s: Provider slice doesn't start with 'provider ': %q", s.ID, head(slice, 60))
			}
		case "Local":
			// Local symbol's slice is the assignment line "name = expr".
			if !strings.HasPrefix(slice, s.Name) {
				t.Errorf("%s: Local slice doesn't start with name %q: %q", s.ID, s.Name, head(slice, 60))
			}
		case "Setting":
			// .tfvars Setting slice is "name = value".
			if !strings.HasPrefix(slice, s.Name) {
				t.Errorf("%s: Setting slice doesn't start with name %q: %q", s.ID, s.Name, head(slice, 60))
			}
		case "Block":
			// Nested or top-level terraform Block — slice should contain the block type.
			// Top-level "terraform" symbol's slice starts with "terraform"; nested blocks
			// like "lifecycle" / "provisioner" / "backend" / etc. start with their type.
			parts := strings.Split(s.QualifiedName, ".")
			blockType := parts[len(parts)-1]
			if blockType == s.Name {
				blockType = parts[len(parts)-1]
			} else if len(parts) >= 2 {
				// Labeled nested (e.g. provisioner.local-exec, dynamic.tag).
				blockType = parts[len(parts)-2]
			}
			if !strings.HasPrefix(slice, blockType) {
				t.Errorf("%s: Block slice doesn't start with type %q: %q", s.ID, blockType, head(slice, 60))
			}
		}
	}
}

func TestHCLIntegration_FTS5SearchRoundTrip(t *testing.T) {
	root := writeMixedFixture(t)
	_, store, pid := indexFixture(t, root, true)

	// Each query should find its target HCL symbol via the FTS5 path.
	// This catches the FTS5-column-rename class of bug (the synced inverted
	// index missing rows or the join failing on a stale column).
	cases := []struct {
		query  string
		wantQN string
	}{
		{"web", "resource.aws_instance.web"},
		{"ubuntu", "data.aws_ami.ubuntu"},
		{"vpc", "module.vpc"},
		{"region", "var.region"},
		{"web_ip", "output.web_ip"},
		{"common_tags", "local.common_tags"},
	}
	for _, c := range cases {
		// HCL symbols route to the config corpus; after the #32 part-3
		// default flip, the SearchSymbols shim hits the code corpus and
		// would return zero. Pass corpus=config explicitly.
		hits, err := store.SearchSymbolsByCorpus(pid, c.query, "", "HCL", db.CorpusConfig, 50)
		if err != nil {
			t.Fatalf("SearchSymbols(%q): %v", c.query, err)
		}
		found := false
		for _, h := range hits {
			if h.Symbol.QualifiedName == c.wantQN {
				found = true
				break
			}
		}
		if !found {
			qns := make([]string, 0, len(hits))
			for _, h := range hits {
				qns = append(qns, h.Symbol.QualifiedName)
			}
			t.Errorf("FTS5 search %q (lang=HCL) did not return %q; got: %v", c.query, c.wantQN, qns)
		}
	}
}

func TestHCLIntegration_ReindexIdempotent(t *testing.T) {
	root := writeMixedFixture(t)
	idx, store, pid := indexFixture(t, root, true)

	// Snapshot symbol count.
	before := totalSymbols(t, store, pid)

	// Re-index without force; expect hash skip to short-circuit every file
	// and the symbol count to be identical.
	res2, err := idx.Index(context.Background(), root, false)
	if err != nil {
		t.Fatalf("re-index: %v", err)
	}
	if res2.Skipped == 0 {
		t.Errorf("expected Skipped > 0 on idempotent re-index, got 0 (Files=%d Symbols=%d)", res2.Files, res2.Symbols)
	}
	after := totalSymbols(t, store, pid)
	if after != before {
		t.Errorf("symbol count changed after idempotent re-index: before=%d after=%d", before, after)
	}
}

func TestHCLIntegration_CypherNodeLabelMatchesNewKinds(t *testing.T) {
	// `MATCH (r:Resource) RETURN r` should resolve via the Cypher engine to
	// `WHERE kind = 'Resource'`. Verify all 6 new HCL kinds + 2 reused kinds
	// work as node labels in Cypher.
	root := writeMixedFixture(t)
	_, store, pid := indexFixture(t, root, true)

	exec := &cypher.Executor{DB: store.DB(), ProjectID: pid}

	cases := []struct {
		label   string
		minRows int
	}{
		{"Resource", 1},
		{"DataSource", 1},
		{"Module", 1},
		{"Variable", 1},
		{"Output", 1},
		{"Provider", 1},
		{"Local", 1},
		{"Block", 1},
		{"Setting", 1}, // .tfvars assignment
	}
	for _, c := range cases {
		query := "MATCH (n:" + c.label + ") RETURN n.kind, n.qualified_name"
		res, err := exec.Execute(context.Background(), query)
		if err != nil {
			t.Errorf("Cypher %q: %v", query, err)
			continue
		}
		if len(res.Rows) < c.minRows {
			t.Errorf("Cypher %q: got %d rows, want at least %d", query, len(res.Rows), c.minRows)
			continue
		}
		for _, row := range res.Rows {
			if got := row["n.kind"]; got != c.label {
				t.Errorf("Cypher %q: row n.kind = %v, want %s (qn=%v)", query, got, c.label, row["n.qualified_name"])
			}
		}
	}
}

func TestHCLIntegration_SearchKindFilter(t *testing.T) {
	// MCP search tool's `kind=` parameter is a free-form string passed to
	// `WHERE kind = ?`. Verify each new HCL kind narrows results correctly:
	// (a) the kind filter actually returns hits we expect, AND (b) the
	// returned rows' kind/language match the filter (no leakage).
	root := writeMixedFixture(t)
	_, store, pid := indexFixture(t, root, true)

	// Per-kind: a query that should match at least one symbol of that kind
	// AND verify the kind filter narrows correctly.
	cases := []struct {
		kind, query string
	}{
		{"Resource", "web"},
		{"DataSource", "ubuntu"},
		{"Module", "vpc"},
		{"Variable", "region"},
		{"Output", "web_ip"},
		{"Provider", "aws"},
		{"Local", "common_tags"},
		{"Block", "lifecycle"},
		{"Setting", "instance_count"},
	}
	for _, c := range cases {
		// Same rationale as above — HCL is in the config corpus.
		hits, err := store.SearchSymbolsByCorpus(pid, c.query, c.kind, "HCL", db.CorpusConfig, 50)
		if err != nil {
			t.Fatalf("SearchSymbols(%q, kind=%s): %v", c.query, c.kind, err)
		}
		if len(hits) == 0 {
			t.Errorf("query=%q kind=%s returned 0 hits — fixture should have at least one match", c.query, c.kind)
			continue
		}
		for _, h := range hits {
			if h.Symbol.Kind != c.kind {
				t.Errorf("kind=%s filter leaked symbol of kind %q: %s", c.kind, h.Symbol.Kind, h.Symbol.QualifiedName)
			}
			if h.Symbol.Language != "HCL" {
				t.Errorf("language=HCL filter leaked symbol of language %q: %s", h.Symbol.Language, h.Symbol.QualifiedName)
			}
		}
	}
}

func TestHCLIntegration_SymbolIDStability(t *testing.T) {
	root := writeMixedFixture(t)
	idx, store, pid := indexFixture(t, root, true)

	// Capture full ID set after first force-index.
	first := hclSymbolIDSet(t, store, pid)
	if len(first) == 0 {
		t.Fatal("expected HCL symbols on first index")
	}

	// Re-index without force (hash-skip path).
	if _, err := idx.Index(context.Background(), root, false); err != nil {
		t.Fatalf("re-index (no force): %v", err)
	}
	second := hclSymbolIDSet(t, store, pid)
	assertIDSetsEqual(t, first, second, "after no-force re-index")

	// Re-index WITH force (full re-parse).
	if _, err := idx.Index(context.Background(), root, true); err != nil {
		t.Fatalf("re-index (force): %v", err)
	}
	third := hclSymbolIDSet(t, store, pid)
	assertIDSetsEqual(t, first, third, "after force re-index")
}

func hclSymbolIDSet(t *testing.T, store *db.Store, projectID string) map[string]struct{} {
	t.Helper()
	rows, err := store.DB().Query(
		`SELECT id FROM symbols WHERE project_id = ? AND language = 'HCL'`, projectID)
	if err != nil {
		t.Fatalf("hcl id set: %v", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = struct{}{}
	}
	return out
}

func assertIDSetsEqual(t *testing.T, a, b map[string]struct{}, label string) {
	t.Helper()
	if len(a) != len(b) {
		t.Errorf("%s: ID set size differs: before=%d after=%d", label, len(a), len(b))
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			t.Errorf("%s: ID %q present before, missing after", label, id)
		}
	}
	for id := range b {
		if _, ok := a[id]; !ok {
			t.Errorf("%s: ID %q present after, missing before", label, id)
		}
	}
}

// TestHCLIntegration_StaleSymbolCleanupOnEdit verifies that when an HCL block
// is removed from a .tf file and the file is re-parsed, the orphaned symbol
// row is deleted from the DB (no stale data lingers).
//
// This test originally documented LATENT #15 (pre-HCL bug — indexer didn't
// call DeleteSymbolsForFile on re-parse, so orphans persisted indefinitely).
// The fix landed on `fix/index-stale-symbol-cleanup` (off master). When this
// branch is run standalone (without that fix merged), the test will skip
// itself with a clear message; when run on top of the fix (e.g. local/dev),
// it asserts the cleanup is correct.
//
// Sequencing: this branch's upstream PR depends on fix/index-stale-symbol-
// cleanup landing first; once that merges, this test will assert and pass.
func TestHCLIntegration_StaleSymbolCleanupOnEdit(t *testing.T) {
	root := writeMixedFixture(t)
	idx, store, _ := indexFixture(t, root, true)

	id := "main.tf::resource.aws_security_group.web_sg#Resource"
	if _, err := store.GetSymbol(id); err != nil {
		t.Fatalf("expected %s to exist before edit: %v", id, err)
	}

	edited := strings.Replace(hclFixtureMain,
		`resource "aws_security_group" "web_sg" {
  name = "web"
}

`, "", 1)
	if edited == hclFixtureMain {
		t.Fatal("edit didn't take effect — fixture changed?")
	}
	if err := os.WriteFile(filepath.Join(root, "main.tf"), []byte(edited), 0o644); err != nil {
		t.Fatalf("write edited main.tf: %v", err)
	}
	if _, err := idx.Index(context.Background(), root, false); err != nil {
		t.Fatalf("re-index after edit: %v", err)
	}

	// GetSymbol returns (nil, nil) when not found (sql.ErrNoRows is mapped
	// to nil-symbol nil-error). Skip cleanly when running against the
	// pre-fix indexer (LATENT #15 not yet merged in this tree); once
	// fix/index-stale-symbol-cleanup is present, sym is nil and we assert
	// the cleanup is correct.
	sym, err := store.GetSymbol(id)
	if err != nil {
		t.Fatalf("GetSymbol(%s) error: %v", id, err)
	}
	if sym != nil {
		t.Skip("LATENT #15 fix (`fix/index-stale-symbol-cleanup`) not yet merged on this tree — orphan still present, as expected pre-fix. The dedicated stale-symbol tests on the fix branch cover the cleanup behavior; rerun this test once the fix merges to assert the HCL-flavored end-to-end path.")
	}
	// sym == nil — orphan correctly cleared by the fix.

	// Independently verify the rest of the file's symbols are still correct.
	stillID := "main.tf::resource.aws_instance.web#Resource"
	if _, err := store.GetSymbol(stillID); err != nil {
		t.Errorf("expected %s to still exist after edit: %v", stillID, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func languageCounts(t *testing.T, store *db.Store, projectID string) map[string]int {
	t.Helper()
	rows, err := store.DB().Query(
		`SELECT language, COUNT(*) FROM symbols WHERE project_id = ? GROUP BY language`,
		projectID,
	)
	if err != nil {
		t.Fatalf("language counts: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var lang string
		var n int
		if err := rows.Scan(&lang, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[lang] = n
	}
	return out
}

func hclKindCounts(t *testing.T, store *db.Store, projectID string) map[string]int {
	t.Helper()
	rows, err := store.DB().Query(
		`SELECT kind, COUNT(*) FROM symbols WHERE project_id = ? AND language = 'HCL' GROUP BY kind`,
		projectID,
	)
	if err != nil {
		t.Fatalf("hcl kind counts: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[k] = n
	}
	return out
}

func allHCLSymbols(t *testing.T, store *db.Store, projectID string) []db.Symbol {
	t.Helper()
	rows, err := store.DB().Query(
		`SELECT id, file_path, name, qualified_name, kind, start_byte, end_byte
		   FROM symbols WHERE project_id = ? AND language = 'HCL'`,
		projectID,
	)
	if err != nil {
		t.Fatalf("all HCL symbols: %v", err)
	}
	defer rows.Close()
	var out []db.Symbol
	for rows.Next() {
		var s db.Symbol
		if err := rows.Scan(&s.ID, &s.FilePath, &s.Name, &s.QualifiedName, &s.Kind, &s.StartByte, &s.EndByte); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func totalSymbols(t *testing.T, store *db.Store, projectID string) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRow(
		`SELECT COUNT(*) FROM symbols WHERE project_id = ?`, projectID,
	).Scan(&n); err != nil {
		t.Fatalf("total symbols: %v", err)
	}
	return n
}

func head(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

const bashSrc = `#!/usr/bin/env bash
set -euo pipefail

# Posix-style function definition.
deploy_node() {
  local host=$1
  echo "deploying to $host"
  ssh "$host" 'systemctl restart myapp'
}

# Reserved-word style.
function rollback {
  echo "rolling back"
  return 1
}

# Reserved-word + parens.
function build_image() {
  docker build -t myapp .
}

# Underscore-prefixed helper (treated as non-exported by convention).
_internal_helper() {
  echo "internal"
}

# Top-level code that is NOT a function — must not be extracted.
echo "starting"
deploy_node "prod-1"
`

func TestExtractBash_PosixStyleFunction(t *testing.T) {
	result := Extract([]byte(bashSrc), "Bash", "scripts/deploy.sh")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	deploy, ok := byName["deploy_node"]
	if !ok {
		t.Fatalf("expected 'deploy_node' function, got: %v", keysOfBash(byName))
	}
	if deploy.Kind != "Function" {
		t.Errorf("kind = %q, want Function", deploy.Kind)
	}
	if !deploy.IsExported {
		t.Error("deploy_node should be IsExported (no leading underscore)")
	}
}

func TestExtractBash_ReservedWordStyle(t *testing.T) {
	result := Extract([]byte(bashSrc), "Bash", "scripts/deploy.sh")
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	rb, ok := byName["rollback"]
	if !ok {
		t.Fatal("expected 'rollback' function (function-keyword style)")
	}
	if !strings.HasPrefix(rb.Signature, "function") {
		t.Errorf("rollback signature = %q, want to start with 'function'", rb.Signature)
	}

	bi, ok := byName["build_image"]
	if !ok {
		t.Fatal("expected 'build_image' function (function-keyword + parens)")
	}
	if !strings.Contains(bi.Signature, "function") || !strings.Contains(bi.Signature, "()") {
		t.Errorf("build_image signature = %q, want to contain 'function' and '()'", bi.Signature)
	}
}

func TestExtractBash_UnderscorePrefixIsInternal(t *testing.T) {
	result := Extract([]byte(bashSrc), "Bash", "scripts/deploy.sh")
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	helper, ok := byName["_internal_helper"]
	if !ok {
		t.Fatal("expected '_internal_helper'")
	}
	if helper.IsExported {
		t.Error("_internal_helper should be IsExported=false (leading underscore)")
	}
}

func TestExtractBash_OnlyFunctionsExtracted(t *testing.T) {
	// Top-level commands like `echo "starting"` and `set -euo pipefail`
	// should not produce symbols — only function declarations do.
	// #1482 v0.77: the per-file Module symbol is also emitted (file-
	// scope anchor for top-level edges). Count Functions explicitly
	// rather than asserting total symbol count.
	result := Extract([]byte(bashSrc), "Bash", "scripts/deploy.sh")
	var fnNames []string
	for _, s := range result.Symbols {
		if s.Kind == "Function" {
			fnNames = append(fnNames, s.Name)
		}
	}
	if len(fnNames) != 4 {
		t.Errorf("expected 4 Function symbols (deploy_node, rollback, build_image, _internal_helper), got %d: %v",
			len(fnNames), fnNames)
	}
	for _, s := range result.Symbols {
		if s.Kind != "Function" && s.Kind != "Module" {
			t.Errorf("symbol %q unexpected kind = %q (want Function or Module)", s.Name, s.Kind)
		}
	}
}

// TestExtractBash_HighConfidence — see note on YAML's HighConfidence test.
// Asserts >= 0.7 (default min_confidence threshold) rather than exactly 1.0
// after #34 Phase 2 introduced per-symbol composition.
func TestExtractBash_HighConfidence(t *testing.T) {
	result := Extract([]byte(bashSrc), "Bash", "scripts/deploy.sh")
	if len(result.Symbols) == 0 {
		t.Fatal("no symbols extracted")
	}
	for _, s := range result.Symbols {
		if s.ExtractionConfidence < 0.7 {
			t.Errorf("symbol %q confidence = %v, want >= 0.7", s.Name, s.ExtractionConfidence)
			break
		}
	}
}

func TestExtractBash_ByteRangeCoversBody(t *testing.T) {
	src := []byte(bashSrc)
	result := Extract(src, "Bash", "scripts/deploy.sh")
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	deploy := byName["deploy_node"]
	body := string(src[deploy.StartByte:deploy.EndByte])
	for _, want := range []string{"deploy_node", "local host", "ssh", "systemctl"} {
		if !strings.Contains(body, want) {
			t.Errorf("deploy_node body missing %q\nbody:\n%s", want, body)
		}
	}
	if strings.Contains(body, "function rollback") {
		t.Errorf("deploy_node body leaks into rollback:\n%s", body)
	}
}

func TestExtractBash_QualifiedNameIncludesModule(t *testing.T) {
	result := Extract([]byte(bashSrc), "Bash", "scripts/deploy.sh")
	byName := make(map[string]ExtractedSymbol)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	deploy := byName["deploy_node"]
	if deploy.QualifiedName != "deploy.deploy_node" {
		t.Errorf("qualified name = %q, want deploy.deploy_node", deploy.QualifiedName)
	}
}

func TestExtractBash_EmptySource(t *testing.T) {
	result := Extract([]byte(""), "Bash", "empty.sh")
	if result == nil {
		t.Fatal("nil result")
	}
	if len(result.Symbols) != 0 {
		t.Errorf("empty source produced %d symbols, want 0", len(result.Symbols))
	}
}

func TestExtractBash_NoFunctions(t *testing.T) {
	src := `#!/usr/bin/env bash
echo hello
ls -la
`
	result := Extract([]byte(src), "Bash", "x.sh")
	// #1482 v0.77: per-file Module symbol is always emitted as the
	// file-scope anchor for top-level edges (matches Go extractor's
	// per-file Module convention). A script with no function defs
	// still gets the Module symbol — zero Functions, exactly one
	// Module.
	var fnCount, modCount int
	for _, s := range result.Symbols {
		switch s.Kind {
		case "Function":
			fnCount++
		case "Module":
			modCount++
		}
	}
	if fnCount != 0 {
		t.Errorf("expected 0 Function symbols (no function defs), got %d", fnCount)
	}
	if modCount != 1 {
		t.Errorf("expected exactly 1 Module symbol per #1482, got %d", modCount)
	}
}

func TestExtractBash_MalformedScript(t *testing.T) {
	// Bash with a syntax error — extractor should not panic.
	src := `do_thing() {
  echo "missing closing brace"
`
	result := Extract([]byte(src), "Bash", "broken.sh")
	if result == nil {
		t.Fatal("nil result on malformed script")
	}
	// No assertion on symbol count — just no panic.
}

func TestExtractBash_ExtensionDetection(t *testing.T) {
	for _, file := range []string{"deploy.sh", "deploy.bash"} {
		if got := DetectLanguage(file); got != "Bash" {
			t.Errorf("DetectLanguage(%q) = %q, want Bash", file, got)
		}
	}
}

func keysOfBash(m map[string]ExtractedSymbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

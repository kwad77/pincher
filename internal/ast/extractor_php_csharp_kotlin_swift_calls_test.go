// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1159 v0.62: per-file CALLS pass extended to PHP / C# / Kotlin / Swift.
// All four use `{` block bodies so the shared regexCallScan path works
// as-is. Same shape as TS/Rust/Java/C — same-file calls resolve,
// cross-file calls drop until per-language resolvers land.
//
// Tests follow the table-from-the-start shape (#1152): positive
// (CALLS emitted) per language, no separate negative cases per
// language because the shared regexCallKeywords blocklist is already
// covered by the Java + TS tests in the parallel files.

const phpWithCallsSrc = `<?php
class Bootstrap {
	public function run() {
		loadConfig();
		$c = parseConfig();
		render($c);
	}
}
`

func TestExtractPHP_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(phpWithCallsSrc), "PHP", "src/boot.php")
	if r == nil {
		t.Fatal("nil result")
	}
	wantTargets := map[string]bool{
		"loadConfig":  false,
		"parseConfig": false,
		"render":      false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" || !strings.HasSuffix(e.FromQN, "run") {
			continue
		}
		if _, expected := wantTargets[e.ToName]; expected {
			wantTargets[e.ToName] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("PHP: expected CALLS edge → %q; missing", target)
		}
	}
}

const csharpWithCallsSrc = `public class Bootstrap {
	public void Run() {
		LoadConfig();
		var c = ParseConfig();
		Render(c);
	}
}
`

func TestExtractCSharp_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(csharpWithCallsSrc), "C#", "src/Bootstrap.cs")
	if r == nil {
		t.Fatal("nil result")
	}
	wantTargets := map[string]bool{
		"LoadConfig":  false,
		"ParseConfig": false,
		"Render":      false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" {
			continue
		}
		if _, expected := wantTargets[e.ToName]; expected {
			wantTargets[e.ToName] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("C#: expected CALLS edge → %q; missing", target)
		}
	}
}

const kotlinWithCallsSrc = `class Bootstrap {
	fun run() {
		loadConfig()
		val c = parseConfig()
		render(c)
	}
}
`

func TestExtractKotlin_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(kotlinWithCallsSrc), "Kotlin", "src/Bootstrap.kt")
	if r == nil {
		t.Fatal("nil result")
	}
	wantTargets := map[string]bool{
		"loadConfig":  false,
		"parseConfig": false,
		"render":      false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" {
			continue
		}
		if _, expected := wantTargets[e.ToName]; expected {
			wantTargets[e.ToName] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("Kotlin: expected CALLS edge → %q; missing", target)
		}
	}
}

const swiftWithCallsSrc = `class Bootstrap {
	func run() {
		loadConfig()
		let c = parseConfig()
		render(c)
	}
}
`

func TestExtractSwift_PerFileCalls_EmitsEdges(t *testing.T) {
	r := Extract([]byte(swiftWithCallsSrc), "Swift", "src/Bootstrap.swift")
	if r == nil {
		t.Fatal("nil result")
	}
	wantTargets := map[string]bool{
		"loadConfig":  false,
		"parseConfig": false,
		"render":      false,
	}
	for _, e := range r.Edges {
		if e.Kind != "CALLS" {
			continue
		}
		if _, expected := wantTargets[e.ToName]; expected {
			wantTargets[e.ToName] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("Swift: expected CALLS edge → %q; missing", target)
		}
	}
}

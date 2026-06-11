// SPDX-License-Identifier: MIT

package ast

import (
	"context"
	"testing"
)

// Self-contained coverage of the real-tree-sitter TS extractor + tsbridge WASM
// path: symbol kinds, funcStack-scoped locals, receiver-type CALLS, imports,
// and the .tsx grammar path via a JSX snippet.
func TestTreeSitterTS_InlineExtract(t *testing.T) {
	const src = `import { z } from "zod";
import http from "node:http";

export interface Repo {
	find(id: number): string;
}

export enum Status { New, Done }

export class Service implements Repo {
	process(): void {
		this.find(1);
		const c: Cart = new Cart();
		c.add(2);
	}
	find(id: number): string { return ""; }
}

export function topLevel(): number {
	function inner() {
		const res = fetch("/x");
	}
	return 1;
}

export const handler = (req: Request) => req.url;
`
	x, err := newTSTSExtractor(context.Background(), false)
	if err != nil {
		t.Fatalf("newTSTSExtractor: %v", err)
	}
	fr := x.extract([]byte(src), "pkg/svc.ts")

	byKind := map[string]map[string]bool{}
	qn := map[string]string{}
	for _, s := range fr.Symbols {
		if byKind[s.Kind] == nil {
			byKind[s.Kind] = map[string]bool{}
		}
		byKind[s.Kind][s.Name] = true
		qn[s.Name] = s.QualifiedName
		if s.ExtractionConfidence != 1.0 {
			t.Errorf("symbol %s: confidence %v, want 1.0", s.Name, s.ExtractionConfidence)
		}
	}
	for name, kind := range map[string]string{
		"Repo": "Interface", "Status": "Enum", "Service": "Class",
		"topLevel": "Function", "handler": "Function",
	} {
		if !byKind[kind][name] {
			t.Errorf("missing %s %q; got %v", kind, name, byKind[kind])
		}
	}
	if !byKind["Method"]["process"] {
		t.Errorf("missing Method process; got %v", byKind["Method"])
	}
	// funcStack-scoped local: res inside inner inside topLevel.
	if qn["res"] != "pkg.svc.topLevel.inner.res" {
		t.Errorf("res QN = %q, want pkg.svc.topLevel.inner.res", qn["res"])
	}
	// Method QN is class-scoped (namespace-blind module base).
	if qn["process"] != "pkg.svc.Service.process" {
		t.Errorf("process QN = %q, want pkg.svc.Service.process", qn["process"])
	}

	imports := map[string]bool{}
	type ck struct{ to, recv string }
	calls := map[ck]bool{}
	for _, e := range fr.Edges {
		switch e.Kind {
		case "IMPORTS":
			imports[e.ToName] = true
		case "CALLS":
			calls[ck{e.ToName, e.ReceiverType}] = true
		}
	}
	for _, want := range []string{"zod", "node:http"} {
		if !imports[want] {
			t.Errorf("import %q missing; got %v", want, imports)
		}
	}
	// Receiver-type-aware CALLS: this.find → Service; c.add → Cart (typed local).
	if !calls[ck{"this.find", "Service"}] {
		t.Errorf("missing CALLS this.find with ReceiverType=Service; got %v", calls)
	}
	if !calls[ck{"c.add", "Cart"}] {
		t.Errorf("missing CALLS c.add with ReceiverType=Cart; got %v", calls)
	}
}

// The .tsx grammar parses JSX without error and still extracts symbols.
func TestTreeSitterTSX_JSXParsesAndExtracts(t *testing.T) {
	const src = `import React from "react";

export function Button(props: Props): JSX.Element {
	return <button onClick={props.onClick}>{props.label}</button>;
}

export const Panel = (p: PanelProps) => <div className="panel">{p.children}</div>;
`
	x, err := newTSTSExtractor(context.Background(), true)
	if err != nil {
		t.Fatalf("newTSTSExtractor(tsx): %v", err)
	}
	fr, ok := x.extractChecked([]byte(src), "ui/Button.tsx")
	if !ok {
		t.Fatal("tsx parse hit an ERROR node on valid JSX")
	}
	names := map[string]bool{}
	for _, s := range fr.Symbols {
		if s.Kind == "Function" {
			names[s.Name] = true
		}
	}
	for _, want := range []string{"Button", "Panel"} {
		if !names[want] {
			t.Errorf("missing TSX Function %q; got %v", want, names)
		}
	}
}

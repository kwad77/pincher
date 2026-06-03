// SPDX-License-Identifier: MIT

package ast

import (
	"strings"
	"testing"
)

// #1208 v0.66 DOGFOOD: TS function overload signatures (declared
// without body, ending in `;`) duplicated the qualified_name of the
// implementation, producing a qualified_name_collision per file. The
// fix drops signatures and keeps only the implementation, mirroring
// dropCForwardDecls's role for C.

// Positive: a 3-signature + 1-implementation overload set produces
// exactly one Function symbol and zero QN collisions.
func TestExtractTS_FunctionOverloads_KeepsImplementationOnly(t *testing.T) {
	t.Parallel()
	source := []byte(`
export function law(a: string): string;
export function law(a: number): number;
export function law(a: any): any {
  return a;
}
`)
	result := extractTypeScript(source, "state-laws.ts")
	lawCount := 0
	for _, s := range result.Symbols {
		if s.Name == "law" && s.Kind == "Function" {
			lawCount++
		}
	}
	if lawCount != 1 {
		t.Errorf("expected exactly 1 Function symbol for law (implementation only); got %d. Symbols: %+v", lawCount, result.Symbols)
	}
	for qn, n := range result.QNCollisions {
		if strings.Contains(qn, "law") && n > 1 {
			t.Errorf("expected no QN collision after overload-signature drop; got %s × %d", qn, n)
		}
	}
}

// Negative (real implementation kept): a single `function name() {...}`
// with no overload signatures must NOT be dropped.
func TestExtractTS_PlainFunctionImplementation_Kept(t *testing.T) {
	t.Parallel()
	source := []byte(`
export function helper(a: number): number {
  return a + 1;
}
`)
	result := extractTypeScript(source, "helper.ts")
	var sawHelper bool
	for _, s := range result.Symbols {
		if s.Name == "helper" && s.Kind == "Function" {
			sawHelper = true
		}
	}
	if !sawHelper {
		t.Errorf("plain function implementation must be kept; got symbols: %+v", result.Symbols)
	}
}

// Cross-check (multi-line signature): `function name(a: number,\n b: string): ReturnType<{x:number}>;`
// spread across multiple lines is still detected as a signature.
func TestExtractTS_MultiLineOverloadSignature_Dropped(t *testing.T) {
	t.Parallel()
	source := []byte(`
export function compose(
  a: number,
  b: string,
): Promise<{ result: number }>;
export function compose(a: any, b: any): any {
  return { result: 0 };
}
`)
	result := extractTypeScript(source, "compose.ts")
	composeCount := 0
	for _, s := range result.Symbols {
		if s.Name == "compose" && s.Kind == "Function" {
			composeCount++
		}
	}
	if composeCount != 1 {
		t.Errorf("multi-line overload signature should be dropped, leaving 1 Function compose; got %d. Symbols: %+v", composeCount, result.Symbols)
	}
}

// Cross-check (arrow function NOT misidentified): `export const helper = (a) => a + 1`
// matches funcRE's arrow alternative — that's NOT a signature/impl pair,
// the overload-signature detector must leave it alone.
func TestExtractTS_ArrowConst_NotMistakenForSignature(t *testing.T) {
	t.Parallel()
	source := []byte(`
export const helper = (a: number): number => a + 1;
`)
	result := extractTypeScript(source, "helper.ts")
	var sawHelper bool
	for _, s := range result.Symbols {
		if s.Name == "helper" {
			sawHelper = true
		}
	}
	if !sawHelper {
		t.Errorf("arrow-function const must not be dropped by overload-signature detector; got symbols: %+v", result.Symbols)
	}
}

// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1693 (#1389 cross-language sweep): C# control-flow keywords satisfy
// the funcRE shape `(?:[\w...]+\s+)+ name \(` when they appear in
// `<token> KEYWORD (` position — overwhelmingly `else if (cond)`,
// where `else` is consumed as a return-type token and `if` lands in
// the name group. Pre-fix every C# file with an `else if` emitted a
// phantom `if` Method; repeated ones collided on the qualified name
// and `pincher doctor` reported `qualified_name_collision` (98 such
// rows across the dogfood corpora, dominated by C/C++/C#).
//
// Four-case shape (#1152): positive (keyword dropped), negative (real
// method preserved), control (Class with keyword-shaped context
// passes), cross-check (name CONTAINING a keyword survives).

// Positive: `else if` + standalone `for`/`while`/`switch`/`lock`
// statements. Pre-fix these lift to phantom Method symbols.
const csharpKeywordFalsePositiveSrc = `namespace Demo;

public class Worker
{
    public void Run(int n)
    {
        if (n > 0)
        {
            DoWork();
        }
        else if (n < 0)
        {
            DoOther();
        }

        for (int i = 0; i < n; i++)
        {
            Step(i);
        }

        while (n > 10)
        {
            n--;
        }

        switch (n)
        {
            default:
                break;
        }

        lock (this)
        {
            n = 0;
        }
    }

    private void DoWork() { }
    private void DoOther() { }
    private void Step(int i) { }
}
`

func TestExtractCSharp_KeywordFalsePositives_Dropped_1693(t *testing.T) {
	t.Parallel()
	result := Extract([]byte(csharpKeywordFalsePositiveSrc), "C#", "src/Worker.cs")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	banned := map[string]bool{
		"if": true, "else": true, "for": true, "while": true,
		"switch": true, "lock": true, "default": true, "break": true,
	}
	for _, s := range result.Symbols {
		if (s.Kind == "Function" || s.Kind == "Method") && banned[s.Name] {
			t.Errorf("keyword %q lifted to a %s symbol — should be filtered (#1693)", s.Name, s.Kind)
		}
	}
}

// Negative: the three real methods (DoWork / DoOther / Step / Run)
// must survive. A too-broad filter would take them too.
func TestExtractCSharp_RealMethodsPreserved_1693(t *testing.T) {
	t.Parallel()
	result := Extract([]byte(csharpKeywordFalsePositiveSrc), "C#", "src/Worker.cs")
	want := map[string]bool{"Run": false, "DoWork": false, "DoOther": false, "Step": false}
	for _, s := range result.Symbols {
		if s.Kind == "Function" || s.Kind == "Method" {
			if _, ok := want[s.Name]; ok {
				want[s.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("real method %q missing — keyword filter is too broad", name)
		}
	}
}

// Control: the Class symbol passes through. The filter only touches
// Function/Method kinds; a class is never keyword-shaped here.
func TestExtractCSharp_ClassNotAffected_1693(t *testing.T) {
	t.Parallel()
	result := Extract([]byte(csharpKeywordFalsePositiveSrc), "C#", "src/Worker.cs")
	var foundClass bool
	for _, s := range result.Symbols {
		if s.Kind == "Class" && s.Name == "Worker" {
			foundClass = true
		}
	}
	if !foundClass {
		t.Error("Worker class symbol missing — the keyword filter must not touch Class kinds")
	}
}

// Cross-check: a method whose name CONTAINS a keyword as a substring
// (`ifMatches`, `forEach`, `whileLoop`, `switchMode`) must survive —
// the filter is exact-name, not substring.
func TestExtractCSharp_KeywordSubstringNamesPreserved_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`namespace Demo;

public class Lookalikes
{
    public bool ifMatches(int x) { return x > 0; }
    public void forEach(int n) { }
    public int whileLoop() { return 0; }
    public void switchMode(string m) { }
}
`)
	result := Extract(src, "C#", "src/Lookalikes.cs")
	want := map[string]bool{"ifMatches": false, "forEach": false, "whileLoop": false, "switchMode": false}
	for _, s := range result.Symbols {
		if s.Kind == "Function" || s.Kind == "Method" {
			if _, ok := want[s.Name]; ok {
				want[s.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("method %q (keyword as substring) was dropped — filter must be exact-name only", name)
		}
	}
}

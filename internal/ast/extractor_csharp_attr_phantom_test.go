// SPDX-License-Identifier: MIT

package ast

import (
	"testing"
)

// #1693 (#1389 cross-language sweep): C# funcRE's return-type token
// class `[\w<>?,\.\[\]]+` included `[`/`]` (for `byte[]` array
// return types). On a multi-attribute line —
// `[SerializeField, Range(0f, 1f)] private float phase2At = ...` —
// the attribute-skip group backtracks to zero, `[SerializeField,`
// is eaten as a return-type token, and the LAST attribute's name
// (`Range`) is captured as a phantom Method. Unity codebases hit
// this on every `[SerializeField, Range(...)]` field → repeated
// phantoms collide on qualified_name. The token must START with a
// word char (a real type never begins with `[`).
//
// Four-case shape (#1152): positive (multi-attribute line emits no
// phantom), negative (real methods + array return types kept),
// control (single attribute with parens — never phantomed — still
// fine), cross-check (the attribute name is not anywhere a Method).

func TestExtractCSharp_MultiAttrPhantomDropped_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class BossController
{
    [SerializeField, Range(0f, 1f)] private float phase2At = 0.66f;
    [SerializeField, Range(0f, 1f)] private float phase3At = 0.33f;
    [Header("Tuning"), Tooltip("min"), Range(1, 99)] public int waves = 3;
}
`)
	result := Extract(src, "C#", "src/BossController.cs")
	if result == nil {
		t.Fatal("Extract returned nil")
	}
	for _, s := range result.Symbols {
		if (s.Kind == "Function" || s.Kind == "Method") &&
			(s.Name == "Range" || s.Name == "Tooltip" || s.Name == "Header" || s.Name == "SerializeField") {
			t.Errorf("attribute name %q captured as a phantom Method (#1693)", s.Name)
		}
	}
}

// Negative: real methods — including ones with array / generic
// return types — must still be captured. The token class still
// allows `[` `]` MID-token, just not as the first char.
func TestExtractCSharp_ArrayAndGenericReturnsKept_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class Codec
{
    public byte[] Encode() { return null; }
    public Task<List<int>> LoadAsync() { return null; }
    public int? TryParse() { return null; }
    [Pure] public string Describe() { return ""; }
}
`)
	result := Extract(src, "C#", "src/Codec.cs")
	got := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "Function" || s.Kind == "Method" {
			got[s.Name] = true
		}
	}
	for _, want := range []string{"Encode", "LoadAsync", "TryParse", "Describe"} {
		if !got[want] {
			t.Errorf("real method %q dropped — first-char restriction broke an array/generic return type; got %v", want, got)
		}
	}
}

// Control: a single attribute carrying parens — `[Tooltip("x")]` —
// was never phantomed (no comma → no token split), and must stay
// that way; the real method on that line is still captured.
func TestExtractCSharp_SingleAttrLineUnaffected_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class Widget
{
    [Tooltip("the label")] public void Render() { }
}
`)
	result := Extract(src, "C#", "src/Widget.cs")
	got := map[string]string{}
	for _, s := range result.Symbols {
		got[s.Name] = s.Kind
	}
	if got["Render"] != "Method" && got["Render"] != "Function" {
		t.Errorf("real method Render not captured behind a single paren-bearing attribute; got %v", got)
	}
	if _, bad := got["Tooltip"]; bad {
		t.Error("Tooltip attribute captured as a symbol")
	}
}

// Cross-check: a method legitimately NAMED like a common attribute
// (`Range`) must still be captured when it is a real declaration.
func TestExtractCSharp_RealMethodNamedLikeAttrKept_1693(t *testing.T) {
	t.Parallel()
	src := []byte(`class Util
{
    public static int Range(int lo, int hi) { return hi - lo; }
}
`)
	result := Extract(src, "C#", "src/Util.cs")
	var found bool
	for _, s := range result.Symbols {
		if (s.Kind == "Function" || s.Kind == "Method") && s.Name == "Range" {
			found = true
		}
	}
	if !found {
		t.Error("a real method declared `Range(int, int)` was dropped — the fix must only suppress the attribute-line phantom")
	}
}

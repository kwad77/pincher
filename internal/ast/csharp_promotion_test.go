// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1459 v0.73 — C# extractor promotion from 0.70 → 0.85 (stable
// regex tier, joining TS/TSX/Rust/Java/Swift/Kotlin). Tests pin
// every shape the promotion covers — emphasis on modern C# 9+
// declarations (records, file-scoped types) and the ubiquitous
// [Attribute] prefix pattern.

func TestCSharp_RecordType(t *testing.T) {
	src := []byte(`public record Person(string Name, int Age);

public record class Employee(string Name, int Age, string Title) : Person(Name, Age);

public record struct Point(int X, int Y);`)
	result := Extract(src, "C#", "Models.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Person", "Employee", "Point"} {
		if s, ok := byName[n]; !ok {
			t.Errorf("expected record %s; got %v", n, csKeysOf(byName))
		} else if s.Kind != "Class" {
			t.Errorf("%s.Kind = %q; want Class (records modeled as Class kind)", n, s.Kind)
		}
	}
}

func TestCSharp_StructType(t *testing.T) {
	src := []byte(`public struct Color {
    public byte R, G, B;
}

public readonly struct ImmutablePoint {
    public readonly int X, Y;
}`)
	result := Extract(src, "C#", "Geometry.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Color", "ImmutablePoint"} {
		if s, ok := byName[n]; !ok {
			t.Errorf("expected struct %s; got %v", n, csKeysOf(byName))
		} else if s.Kind != "Class" {
			t.Errorf("%s.Kind = %q; want Class", n, s.Kind)
		}
	}
}

func TestCSharp_EnumType(t *testing.T) {
	src := []byte(`public enum LogLevel {
    Debug,
    Info,
    Warn,
    Error
}

[Flags]
public enum FileAccess {
    Read = 1,
    Write = 2,
    ReadWrite = Read | Write
}`)
	result := Extract(src, "C#", "Enums.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"LogLevel", "FileAccess"} {
		if s, ok := byName[n]; !ok {
			t.Errorf("expected enum %s; got %v", n, csKeysOf(byName))
		} else if s.Kind != "Enum" {
			t.Errorf("%s.Kind = %q; want Enum", n, s.Kind)
		}
	}
}

func TestCSharp_AttributePrefixOnClass(t *testing.T) {
	src := []byte(`[Serializable]
public class Config {}

[ApiController]
[Route("api/[controller]")]
public class UsersController : ControllerBase {}

[DataContract] public class Pet {}`)
	result := Extract(src, "C#", "Web.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Config", "UsersController", "Pet"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected [Attribute]-prefixed class %s; got %v", n, csKeysOf(byName))
		}
	}
}

func TestCSharp_AttributePrefixOnMethod(t *testing.T) {
	src := []byte(`public class UsersController {
    [HttpGet("/api/users")]
    public IActionResult ListUsers() { return Ok(); }

    [HttpPost("/api/users")] public async Task<IActionResult> CreateUser(User u) { return Ok(); }

    [Authorize(Roles = "Admin")]
    public IActionResult Delete(int id) { return Ok(); }
}`)
	result := Extract(src, "C#", "Controllers.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, m := range []string{"ListUsers", "CreateUser", "Delete"} {
		if s, ok := byName[m]; !ok {
			t.Errorf("expected [Attribute]-prefixed method %s; got %v", m, csKeysOf(byName))
		} else if s.Kind != "Method" {
			t.Errorf("%s.Kind = %q; want Method (scoped to UsersController)", m, s.Kind)
		}
	}
}

func TestCSharp_PartialClass(t *testing.T) {
	src := []byte(`public partial class GeneratedCode {
    public void GeneratedMethod() {}
}

public partial class GeneratedCode {
    public void HandWrittenMethod() {}
}

public partial interface IPartialInterface {
    void Method1();
}`)
	result := Extract(src, "C#", "Partial.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["GeneratedCode"]; !ok {
		t.Errorf("expected partial class GeneratedCode; got %v", csKeysOf(byName))
	}
	if _, ok := byName["IPartialInterface"]; !ok {
		t.Errorf("expected partial interface IPartialInterface; got %v", csKeysOf(byName))
	}
}

func TestCSharp_FileScopedAccess(t *testing.T) {
	// C# 11+ `file` access modifier — restricts type to file scope.
	src := []byte(`file class FileLocalHelper {
    public string Compute() => "x";
}

file record FileLocalRecord(int Value);`)
	result := Extract(src, "C#", "FileLocal.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"FileLocalHelper", "FileLocalRecord"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected file-scoped %s; got %v", n, csKeysOf(byName))
		}
	}
}

func TestCSharp_GenericTypes(t *testing.T) {
	src := []byte(`public class Cache<TKey, TValue> where TKey : notnull {
    public TValue Get(TKey key) => default!;
}

public interface IRepository<T> where T : class, new() {
    Task<T> GetByIdAsync(int id);
}

public record Pair<TFirst, TSecond>(TFirst First, TSecond Second);`)
	result := Extract(src, "C#", "Generic.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Cache", "IRepository", "Pair"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected generic type %s; got %v", n, csKeysOf(byName))
		}
	}
}

func TestCSharp_ModernModifiers(t *testing.T) {
	src := []byte(`public class Service {
    public required string Name { get; init; }
    public virtual void Process() {}
    public override string ToString() => Name;
    public async Task<int> ComputeAsync() => 42;
    public static void Reset() {}
    public sealed override int GetHashCode() => 0;
}`)
	result := Extract(src, "C#", "Service.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, m := range []string{"Process", "ToString", "ComputeAsync", "Reset", "GetHashCode"} {
		if _, ok := byName[m]; !ok {
			t.Errorf("expected modified method %s; got %v", m, csKeysOf(byName))
		}
	}
}

func TestCSharp_ExistingBehaviour_Regression(t *testing.T) {
	// Pre-#1459 existing TestExtractCSharp shape — must keep working.
	src := []byte(`using System;

public class OrderService : IService {
    private readonly string _name;

    public OrderService(string name) {
        _name = name;
    }

    public async Task<string> GetOrderAsync(int id) {
        return id.ToString();
    }

    private void Validate() {}
}

public interface IService {
    Task<string> GetOrderAsync(int id);
}`)
	result := Extract(src, "C#", "Services/OrderService.cs")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	// OrderService appears twice — once as Class (line 3) and once
	// as a Method-shaped constructor (line 6). Iterate over all
	// symbols since byName-map keeps only the last entry per name.
	var classFound, ifaceFound bool
	for _, s := range result.Symbols {
		if s.Name == "OrderService" && s.Kind == "Class" {
			classFound = true
		}
		if s.Name == "IService" && s.Kind == "Interface" {
			ifaceFound = true
		}
	}
	if !classFound {
		t.Errorf("OrderService Class not found in %v", csKeysOf(byName))
	}
	if !ifaceFound {
		t.Errorf("IService Interface not found in %v", csKeysOf(byName))
	}
	if _, ok := byName["GetOrderAsync"]; !ok {
		t.Error("expected method GetOrderAsync")
	}
}

func TestCSharp_ExtractorConfidenceIs085(t *testing.T) {
	if c := RegisteredConfidence("C#"); c != 0.85 {
		t.Errorf("C# registry confidence = %v; want 0.85 (#1459 promotion)", c)
	}
}

func csKeysOf(m map[string]ExtractedSymbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

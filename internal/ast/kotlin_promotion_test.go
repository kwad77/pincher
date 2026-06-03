// SPDX-License-Identifier: MIT

package ast

import "testing"

// #1457 v0.73 — Kotlin extractor promotion from 0.70 → 0.85 (stable
// regex tier, joining TS/TSX/Rust/Java/Swift). These tests pin every
// shape the promotion covers.

func TestKotlin_DataClassIsExtractedAsClass(t *testing.T) {
	src := []byte(`data class User(val name: String, val age: Int) {
    fun greet() = "Hello $name"
}`)
	result := Extract(src, "Kotlin", "src/User.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if u, ok := byName["User"]; !ok || u.Kind != "Class" {
		t.Errorf("expected data class User as Class; got %v", kotlinKeysOf(byName))
	}
	if g, ok := byName["greet"]; !ok || g.Kind != "Method" {
		t.Errorf("expected method greet scoped to User; got Kind=%q Parent=%q", g.Kind, g.Parent)
	}
}

func TestKotlin_SealedClass(t *testing.T) {
	src := []byte(`sealed class Result {
    class Success(val data: String) : Result()
    class Failure(val err: String) : Result()
    object Loading : Result()
}`)
	result := Extract(src, "Kotlin", "src/Result.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Result", "Success", "Failure", "Loading"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected %s; got %v", n, kotlinKeysOf(byName))
		}
	}
}

func TestKotlin_ObjectSingleton(t *testing.T) {
	src := []byte(`object Logger {
    fun info(msg: String) {}
    fun error(msg: String) {}
}`)
	result := Extract(src, "Kotlin", "src/Logger.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if l, ok := byName["Logger"]; !ok || l.Kind != "Class" {
		t.Errorf("expected object Logger as Class; got %v", kotlinKeysOf(byName))
	}
	for _, m := range []string{"info", "error"} {
		s, ok := byName[m]
		if !ok || s.Kind != "Method" || s.Parent == "" {
			t.Errorf("expected method %s scoped to Logger; got %v Kind=%q Parent=%q", m, ok, s.Kind, s.Parent)
		}
	}
}

func TestKotlin_CompanionObject(t *testing.T) {
	src := []byte(`class Cache {
    companion object Factory {
        fun create(): Cache = Cache()
    }
}`)
	result := Extract(src, "Kotlin", "src/Cache.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["Cache"]; !ok {
		t.Errorf("expected class Cache; got %v", kotlinKeysOf(byName))
	}
	if _, ok := byName["Factory"]; !ok {
		t.Errorf("expected companion object Factory; got %v", kotlinKeysOf(byName))
	}
}

func TestKotlin_Interface(t *testing.T) {
	src := []byte(`interface Drawable {
    fun draw()
    fun size(): Int
}

fun interface OnClickListener {
    fun onClick()
}

sealed interface Event {
    object Click : Event
    data class Drag(val x: Int) : Event
}`)
	result := Extract(src, "Kotlin", "src/Interfaces.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Drawable", "OnClickListener", "Event"} {
		s, ok := byName[n]
		if !ok {
			t.Errorf("expected interface %s; got %v", n, kotlinKeysOf(byName))
			continue
		}
		if s.Kind != "Interface" {
			t.Errorf("%s.Kind = %q; want Interface", n, s.Kind)
		}
	}
}

func TestKotlin_EnumClass(t *testing.T) {
	src := []byte(`enum class Direction {
    NORTH, SOUTH, EAST, WEST;

    fun opposite(): Direction = when (this) {
        NORTH -> SOUTH
        SOUTH -> NORTH
        EAST -> WEST
        WEST -> EAST
    }
}`)
	result := Extract(src, "Kotlin", "src/Direction.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	d, ok := byName["Direction"]
	if !ok {
		t.Fatalf("expected enum class Direction; got %v", kotlinKeysOf(byName))
	}
	if d.Kind != "Enum" {
		t.Errorf("Direction.Kind = %q; want Enum", d.Kind)
	}
	if op, ok := byName["opposite"]; !ok || op.Kind != "Method" {
		t.Errorf("expected method opposite scoped to Direction; got Kind=%q", op.Kind)
	}
}

func TestKotlin_AtAnnotationPrefix(t *testing.T) {
	src := []byte(`@Composable
@OptIn(ExperimentalApi::class)
fun ContentView(text: String) {}

@Throws(IOException::class)
fun readFile(path: String): String = ""

@JvmStatic
fun staticHelper() {}`)
	result := Extract(src, "Kotlin", "src/Annotated.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"ContentView", "readFile", "staticHelper"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected @annotated fun %s; got %v", n, kotlinKeysOf(byName))
		}
	}
}

func TestKotlin_AnnotatedClass(t *testing.T) {
	src := []byte(`@Serializable
data class Config(val key: String)

@JvmInline
value class UserId(val value: Long)

@Suppress("DEPRECATION")
class LegacyHandler {}`)
	result := Extract(src, "Kotlin", "src/Annotated.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Config", "UserId", "LegacyHandler"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected @annotated class %s; got %v", n, kotlinKeysOf(byName))
		}
	}
}

func TestKotlin_OverrideAndModifiers(t *testing.T) {
	src := []byte(`abstract class Base {
    abstract fun process(): String
    open fun describe(): String = "base"
}

class Impl : Base() {
    override fun process(): String = "impl"
    override fun describe(): String = "impl"
    inline fun fastPath() = run { }
    tailrec fun recurse(n: Int): Int = if (n <= 0) 0 else recurse(n - 1)
}`)
	result := Extract(src, "Kotlin", "src/Impl.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Base", "Impl", "process", "describe", "fastPath", "recurse"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected %s; got %v", n, kotlinKeysOf(byName))
		}
	}
}

func TestKotlin_OperatorAndInfix(t *testing.T) {
	src := []byte(`data class Vec(val x: Int, val y: Int) {
    operator fun plus(o: Vec) = Vec(x + o.x, y + o.y)
    infix fun dot(o: Vec) = x * o.x + y * o.y
}`)
	result := Extract(src, "Kotlin", "src/Vec.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Vec", "plus", "dot"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected %s; got %v", n, kotlinKeysOf(byName))
		}
	}
}

func TestKotlin_SuspendAndCoroutines(t *testing.T) {
	src := []byte(`class Repository {
    suspend fun fetchUser(id: Long): User = TODO()
    suspend inline fun <reified T> fetchTyped(id: Long): T = TODO()
}`)
	result := Extract(src, "Kotlin", "src/Repository.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, n := range []string{"Repository", "fetchUser", "fetchTyped"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("expected %s; got %v", n, kotlinKeysOf(byName))
		}
	}
}

func TestKotlin_GenericFunctionTypeParameters(t *testing.T) {
	src := []byte(`fun <T> identity(x: T): T = x
fun <T : Comparable<T>> max(a: T, b: T): T = if (a > b) a else b
inline fun <reified T> typeOf(): String = T::class.simpleName ?: ""`)
	result := Extract(src, "Kotlin", "src/Generic.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, fn := range []string{"identity", "max", "typeOf"} {
		if _, ok := byName[fn]; !ok {
			t.Errorf("expected generic fun %s; got %v", fn, kotlinKeysOf(byName))
		}
	}
}

func TestKotlin_ExtensionFunctionRegression(t *testing.T) {
	// #1183 v0.67 — `fun Type.method()` must capture `method` as the
	// name (not `Type`). Pre-#1183 the extractor emitted fake "String"/
	// "List"/etc symbols. Regression guard.
	src := []byte(`fun String.uppercase(): String = ""
fun <T> Iterable<T>.firstOrNull(): T? = null
fun List<String>.uniq(): List<String> = distinct()`)
	result := Extract(src, "Kotlin", "src/Ext.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	for _, fn := range []string{"uppercase", "firstOrNull", "uniq"} {
		if _, ok := byName[fn]; !ok {
			t.Errorf("expected extension fun %s (real name, not receiver type); got %v", fn, kotlinKeysOf(byName))
		}
	}
	// Negative — should NOT contain the receiver-type names.
	for _, falseName := range []string{"String", "Iterable", "List"} {
		if s, ok := byName[falseName]; ok && s.Kind == "Function" {
			t.Errorf("receiver-type name %q leaked as Function symbol", falseName)
		}
	}
}

func TestKotlin_ExpectActualMultiplatform(t *testing.T) {
	src := []byte(`expect class Platform {
    fun name(): String
}

actual class Platform {
    actual fun name(): String = "JVM"
}

expect fun currentTime(): Long
actual fun currentTime(): Long = System.currentTimeMillis()`)
	result := Extract(src, "Kotlin", "src/Platform.kt")
	if result == nil {
		t.Fatal("nil result")
	}
	byName := map[string]ExtractedSymbol{}
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}
	if _, ok := byName["Platform"]; !ok {
		t.Errorf("expected Platform class; got %v", kotlinKeysOf(byName))
	}
	if _, ok := byName["currentTime"]; !ok {
		t.Errorf("expected expect/actual fun currentTime; got %v", kotlinKeysOf(byName))
	}
}

// Confidence promotion guard.
func TestKotlin_ExtractorConfidenceIs085(t *testing.T) {
	if c := RegisteredConfidence("Kotlin"); c != 0.85 {
		t.Errorf("Kotlin registry confidence = %v; want 0.85 (#1457 promotion)", c)
	}
}

func kotlinKeysOf(m map[string]ExtractedSymbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

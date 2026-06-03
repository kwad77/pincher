// SPDX-License-Identifier: MIT

package ast

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Extractor is the interface every per-language symbol extractor satisfies.
//
// One extractor may handle multiple closely-related languages (e.g. "JavaScript"
// and "JSX" share a regex set, "YAML" and "JSON" share a yaml.v3 parser). The
// registry routes by language name; the public Extract / ExtractWithModule
// entry points look up the extractor for the requested language and call it.
//
// Adding a new language is one file: implement Extractor, register in init().
type Extractor interface {
	// Languages returns every language name this extractor handles. Names must
	// match what DetectLanguage returns (e.g. "Markdown", not "md").
	Languages() []string

	// Extensions returns a map from file extension (lowercase, leading dot)
	// to the language name DetectLanguage should return for that extension.
	// e.g. {".md": "Markdown", ".mdx": "Markdown"}.
	Extensions() map[string]string

	// Confidence is the extraction reliability score (0.0–1.0) the dispatcher
	// stamps onto every symbol this extractor produces. 1.0 = AST-exact;
	// 0.85 = stable regex; 0.70 = approximate regex.
	Confidence() float64

	// Extract parses source and returns symbols, edges, and module name.
	// language is the resolved language name (in case an extractor handles
	// multiple). relPath is the file path relative to the project root.
	// opts carries optional context (e.g. ModulePath for Go).
	Extract(source []byte, language, relPath string, opts ExtractOptions) *FileResult
}

// ExtractOptions carries optional context passed from the indexer through to
// extractors. Most extractors ignore it; Go uses ModulePath to rewrite
// intra-module import paths.
type ExtractOptions struct {
	// ModulePath is the value of the `module` line in go.mod, used by the Go
	// extractor to emit within-module IMPORTS edges. Empty otherwise.
	ModulePath string
}

// FilenameExtractor is an optional extension some extractors implement
// to claim files by exact basename rather than extension — e.g. `Makefile`
// or `Dockerfile`, which carry no extension at all but have a stable
// filename convention. Implementing this is purely additive: extractors
// that don't need filename detection still satisfy the bare Extractor
// interface and don't have to opt in.
type FilenameExtractor interface {
	Extractor
	// Filenames returns a map from exact basename (case-sensitive on
	// Unix, case-insensitive in practice on macOS/Windows; the registry
	// stores both forms verbatim and looks up the literal first, then
	// the lowercased fallback) to the language name DetectLanguage
	// should return. e.g. {"Makefile": "Makefile", "GNUmakefile": "Makefile"}.
	Filenames() map[string]string
}

var (
	registryMu  sync.RWMutex
	extractors  []Extractor
	byLanguage  = map[string]Extractor{}
	byExtension = map[string]string{} // extension (".md") → language ("Markdown")
	byFilename  = map[string]string{} // basename ("Makefile") → language ("Makefile")
)

// Register adds an extractor to the registry. Called from init() in each
// per-language file. Later registrations for a language overwrite earlier
// ones, which lets tests substitute extractors if needed.
func Register(e Extractor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	extractors = append(extractors, e)
	for _, lang := range e.Languages() {
		byLanguage[lang] = e
	}
	for ext, lang := range e.Extensions() {
		byExtension[strings.ToLower(ext)] = lang
	}
	if fe, ok := e.(FilenameExtractor); ok {
		for name, lang := range fe.Filenames() {
			byFilename[name] = lang
		}
	}
}

// extractorFor returns the registered Extractor for a language name, or nil.
func extractorFor(language string) Extractor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return byLanguage[language]
}

// languageForExtension returns the language name registered for a file
// extension (with leading dot, case-insensitive), or "" if unsupported.
func languageForExtension(ext string) string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return byExtension[strings.ToLower(ext)]
}

// languageForFilename returns the language name registered for an exact
// basename (e.g. "Makefile"), or "" if no FilenameExtractor claims it.
// Tries the literal first, then the lowercased form so case-insensitive
// filesystems (macOS, Windows) still match `makefile` to `Makefile`.
func languageForFilename(base string) string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	if lang := byFilename[base]; lang != "" {
		return lang
	}
	// Case-insensitive fallback. Only walks the (small) filename map
	// when the literal miss fired — extension matching is unaffected.
	lower := strings.ToLower(base)
	for name, lang := range byFilename {
		if strings.ToLower(name) == lower {
			return lang
		}
	}
	return ""
}

// DetectLanguage returns the language name for a filename. Filenames
// matching a FilenameExtractor (`Makefile`, `Dockerfile`, etc.) take
// precedence over extension lookups so `Makefile.template` style files
// don't get mis-claimed by .template handlers in the future. Returns
// "" for unsupported files.
func DetectLanguage(filename string) string {
	base := filepath.Base(filename)
	if lang := languageForFilename(base); lang != "" {
		return lang
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return ""
	}
	return languageForExtension(ext)
}

// IsSourceFile returns true if the file extension represents an indexable
// source file (i.e. some registered extractor handles it).
func IsSourceFile(filename string) bool {
	return DetectLanguage(filename) != ""
}

// RegisteredConfidence returns the registered Extractor.Confidence() for a
// language, or -1 if no extractor handles that language. Use this when you
// need parser identity (AST vs Regex) rather than per-symbol average
// confidence — symbol-level scores get reduced by path penalties and other
// signals, so averaging them blurs the line between AST and stable-regex
// extractors. The registered value is the extractor's self-declared parser
// quality and never moves.
func RegisteredConfidence(language string) float64 {
	registryMu.RLock()
	defer registryMu.RUnlock()
	e, ok := byLanguage[language]
	if !ok {
		return -1
	}
	return e.Confidence()
}

// SupportedLanguages returns every language name registered, sorted for
// stable output.
func SupportedLanguages() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(byLanguage))
	for lang := range byLanguage {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

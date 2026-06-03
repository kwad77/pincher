// SPDX-License-Identifier: MIT

package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #672 workstream 4 (v0.79 hardening) + CLAUDE.md `## Idioms`:
//
//   > Logging: slog everywhere. log.Printf will silence under bench
//   > TestMain and corrupt baselines.
//
// Per the convention, production code must use slog.* for runtime
// logs. log.Fatalf is allowed for startup-error exits where the
// process is about to die anyway and slog has no Fatal equivalent;
// log.SetOutput / log.SetFlags configure the package logger when
// slog isn't yet wired (pre-main startup paths). What's NOT allowed
// is log.Printf / log.Print / log.Println for runtime events —
// those silently drop under bench TestMain, corrupting baseline
// captures.
//
// Pre-fix one site existed (`cmd/pinch/main.go:286` —
// `log.Printf("pincherMCP: http server error: %v", err)`). Now
// zero. This test pins zero recurrences.

func TestProductionCode_NoLogPrintfPattern(t *testing.T) {
	t.Parallel()

	// Forbidden runtime-logging shapes. Each must NOT appear in
	// production code. log.Fatalf / log.SetOutput / log.SetFlags
	// stay legal.
	forbiddenRE := regexp.MustCompile(`\blog\.(Printf|Print|Println)\b`)

	var violations []string
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// Test files are exempt — they can use log.Printf for
			// debug output, and TestMain wiring sometimes routes
			// log through to stderr deliberately.
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(b)
			matches := forbiddenRE.FindAllStringIndex(text, -1)
			for _, m := range matches {
				line := 1 + strings.Count(text[:m[0]], "\n")
				violations = append(violations, path+":"+lineNumStr(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	if len(violations) > 0 {
		t.Errorf("found %d log.{Printf,Print,Println} call(s) in production code — use slog.{Error,Warn,Info} instead per CLAUDE.md §Idioms (log.Printf silences under bench TestMain and corrupts baseline captures):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func lineNumStr(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}

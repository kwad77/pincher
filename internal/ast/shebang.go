// SPDX-License-Identifier: MIT

package ast

import (
	"bytes"
	"path/filepath"
	"strings"
)

// #262 — shebang fallback for shell scripts that ship without `.sh` /
// `.bash` extensions. OpenWrt uses `.init`, `.hotplug`, `.preinit`;
// Debian packaging uses `.preinst` / `.postinst` / `.postrm`; Linux
// system files like `.login` and extensionless executables in `bin/`
// are common too. Each of these would parse perfectly via mvdan/sh
// at confidence 1.0 — they're just hidden from the indexer because
// the extension lookup doesn't match.
//
// The fallback is two-stage:
//   1. MayHaveShebang(path) — cheap path predicate. True for genuinely
//      extensionless files OR files whose extension is in a small
//      script-likely set. Used by the indexer to decide whether a
//      256-byte content peek is worth doing.
//   2. DetectShebangLanguage(line) — content predicate. Given the
//      first line of a file, returns the language name when a
//      recognized shebang is present, "" otherwise.
//
// Conservative scope: only Bash gets the shebang fallback today.
// Python / Ruby / Perl shebang detection is a follow-up if there's
// appetite — separate issue, similar pattern.

// shebangCandidateExts is the small set of extensions that frequently
// carry a shell shebang in the wild. Files matching one of these get
// a shebang peek; everything else with an unrecognized extension is
// skipped without a content read.
var shebangCandidateExts = map[string]bool{
	".init":     true, // OpenWrt procd init scripts
	".hotplug":  true, // OpenWrt hotplug events
	".preinit":  true, // OpenWrt preinit
	".login":    true, // captive portal login scripts
	".preinst":  true, // Debian package pre-install
	".postinst": true, // Debian package post-install
	".prerm":    true, // Debian package pre-remove
	".postrm":   true, // Debian package post-remove
	".in":       true, // autoconf-generated shell wrappers
	".sh.in":    true, // .sh that's processed by autoconf — extension is ".in"
}

// MayHaveShebang returns true when filename is a candidate for shebang
// detection — extensionless OR with an extension in the
// shebangCandidateExts set. Used to bound the cost of the fallback:
// the indexer only reads a 256-byte peek for these files, not for
// every unrecognized extension in the project.
//
// FilenameExtractor matches (Makefile, Dockerfile, etc.) are already
// handled by DetectLanguage and don't need this path. Files with a
// recognized extension also don't need it — DetectLanguage handles
// them on the fast path.
func MayHaveShebang(filename string) bool {
	base := filepath.Base(filename)
	// Already-claimed by name (Makefile, Dockerfile) — caller's
	// fast-path handles those.
	if languageForFilename(base) != "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(base))
	if ext == "" {
		// Extensionless file. Real-world matches: bin/entrypoint,
		// scripts/setup, /etc/init.d/cron — all candidates.
		return true
	}
	if shebangCandidateExts[ext] {
		return true
	}
	return false
}

// DetectShebangLanguage returns the language name for a file whose
// first line carries a recognized shebang. Returns "" when the line
// is missing the `#!` prefix or when the interpreter isn't one of
// the supported shells.
//
// Recognized forms:
//
//	#!/bin/sh
//	#!/bin/bash
//	#!/usr/bin/env sh
//	#!/usr/bin/env bash
//	#!/usr/bin/env -S bash       (Linux >= 4.10 split args)
//	#!/usr/local/bin/bash
//	#!/usr/bin/dash              (Debian default /bin/sh)
//	#!/bin/ash                   (BusyBox/OpenWrt)
//	#!/bin/ksh                   (Korn shell — close-enough to bash)
//	#!/bin/zsh                   (z shell — Bash extractor handles it best)
//
// Trailing arguments to the interpreter (e.g. `bash -e`) are
// tolerated. CRLF line endings are tolerated. Whitespace before the
// shebang is NOT tolerated — the `#!` must be at byte 0.
func DetectShebangLanguage(firstLine string) string {
	// Strip a trailing CR (CRLF line endings) before further
	// inspection so `#!/bin/bash\r` doesn't fail the suffix check.
	firstLine = strings.TrimRight(firstLine, "\r\n ")
	if !strings.HasPrefix(firstLine, "#!") {
		return ""
	}
	rest := strings.TrimSpace(firstLine[2:])
	if rest == "" {
		return ""
	}

	// First whitespace-separated token is the interpreter path. For
	// `/usr/bin/env [-S] interp`, the second token is the real
	// interpreter — handle that explicitly.
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return ""
	}
	interp := parts[0]
	if filepath.Base(interp) == "env" && len(parts) >= 2 {
		// Skip optional `-S` (split-args) flag.
		next := parts[1]
		if next == "-S" && len(parts) >= 3 {
			next = parts[2]
		}
		interp = next
	}

	switch filepath.Base(interp) {
	case "sh", "bash", "dash", "ash", "ksh", "zsh":
		return "Bash"
	}
	return ""
}

// DetectLanguageFromContent extends DetectLanguage with content
// awareness. When the path-based detection fails, peek the first
// line of content for a shebang. Used by the indexer (#262) so
// .init / .hotplug / .login / extensionless shell scripts get
// indexed by the Bash extractor at confidence 1.0 instead of being
// silently skipped.
//
// content can be the full file body or just the first ~256 bytes —
// only the first line is consulted.
func DetectLanguageFromContent(filename string, content []byte) string {
	if lang := DetectLanguage(filename); lang != "" {
		return lang
	}
	if !MayHaveShebang(filename) {
		return ""
	}
	// First line: bytes up to the first newline. Bound to 512 bytes
	// so a binary blob without a newline doesn't allocate the world.
	const maxFirstLine = 512
	end := len(content)
	if end > maxFirstLine {
		end = maxFirstLine
	}
	first := content[:end]
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	return DetectShebangLanguage(string(first))
}

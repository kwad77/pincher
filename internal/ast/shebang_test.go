// SPDX-License-Identifier: MIT

package ast

import "testing"

// Tests for #262 shebang fallback: shell scripts that ship with
// non-standard extensions (.init, .hotplug, .login) or no extension
// at all must be indexed by the Bash extractor.

func TestDetectShebangLanguage_RecognizedShebangs(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"plain bin sh", "#!/bin/sh", "Bash"},
		{"plain bin bash", "#!/bin/bash", "Bash"},
		{"env bash", "#!/usr/bin/env bash", "Bash"},
		{"env sh", "#!/usr/bin/env sh", "Bash"},
		{"env -S bash", "#!/usr/bin/env -S bash", "Bash"},
		{"local bash", "#!/usr/local/bin/bash", "Bash"},
		{"dash", "#!/usr/bin/dash", "Bash"},
		{"ash (busybox)", "#!/bin/ash", "Bash"},
		{"ksh", "#!/bin/ksh", "Bash"},
		{"zsh", "#!/bin/zsh", "Bash"},
		{"with args", "#!/bin/bash -e", "Bash"},
		{"crlf line ending", "#!/bin/bash\r", "Bash"},
		{"trailing spaces", "#!/bin/bash   ", "Bash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectShebangLanguage(c.line); got != c.want {
				t.Errorf("DetectShebangLanguage(%q) = %q, want %q", c.line, got, c.want)
			}
		})
	}
}

func TestDetectShebangLanguage_NotShebang(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"no shebang", "echo hello"},
		{"hash but no bang", "# this is a comment"},
		{"empty", ""},
		{"whitespace before shebang", "  #!/bin/bash"}, // shebang must be at byte 0
		{"unknown interp", "#!/usr/bin/python3"},
		{"env unknown interp", "#!/usr/bin/env python"},
		{"only shebang prefix", "#!"},
		{"shebang with no interp", "#!   "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectShebangLanguage(c.line); got != "" {
				t.Errorf("DetectShebangLanguage(%q) = %q, want \"\"", c.line, got)
			}
		})
	}
}

func TestMayHaveShebang(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		// Yes: extensionless OR script-like extension
		{"extensionless executable", "bin/entrypoint", true},
		{"openwrt init", "files/travelmate.init", true},
		{"openwrt hotplug", "files/25-travelmate.hotplug", true},
		{"openwrt preinit", "boot/early.preinit", true},
		{"captive portal login", "files/vodafone.login", true},
		{"debian preinst", "debian/myproj.preinst", true},
		{"debian postrm", "debian/myproj.postrm", true},
		{"autoconf .in", "src/wrapper.in", true},
		// No: known script extension (already handled by extension lookup)
		{".sh", "scripts/build.sh", false},
		{".bash", "scripts/run.bash", false},
		// No: known non-script extension
		{".go", "main.go", false},
		{".py", "main.py", false},
		{".md", "README.md", false},
		{".png", "logo.png", false},
		// No: filename match (Makefile is claimed by FilenameExtractor; skip the peek)
		{"Makefile", "Makefile", false},
		// Dockerfile is extensionless and not currently claimed by a
		// FilenameExtractor, so MayHaveShebang says yes. The peek
		// will return "" because Dockerfiles start with "FROM …",
		// not a shebang — wasted effort, no false positive.
		{"Dockerfile", "Dockerfile", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MayHaveShebang(c.path); got != c.want {
				t.Errorf("MayHaveShebang(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestDetectLanguageFromContent_ShebangFallback(t *testing.T) {
	// .init file with bash shebang → routes to Bash via shebang fallback.
	got := DetectLanguageFromContent("files/travelmate.init", []byte("#!/bin/sh\n. /lib/functions.sh\nstart()\n"))
	if got != "Bash" {
		t.Errorf("DetectLanguageFromContent(.init with shebang) = %q, want Bash", got)
	}

	// Extensionless file with env bash shebang.
	got = DetectLanguageFromContent("bin/entrypoint", []byte("#!/usr/bin/env bash\nset -euo pipefail\n"))
	if got != "Bash" {
		t.Errorf("DetectLanguageFromContent(extensionless) = %q, want Bash", got)
	}

	// .init with unrecognized shebang → empty.
	got = DetectLanguageFromContent("files/myperl.in", []byte("#!/usr/bin/perl\nuse strict;\n"))
	if got != "" {
		t.Errorf("DetectLanguageFromContent(.in perl) = %q, want \"\"", got)
	}

	// .init with no shebang → empty.
	got = DetectLanguageFromContent("files/notscript.init", []byte("just plain config text\n"))
	if got != "" {
		t.Errorf("DetectLanguageFromContent(.init no shebang) = %q, want \"\"", got)
	}

	// Path-based detection still wins for files with recognized extensions.
	got = DetectLanguageFromContent("main.go", []byte("package main\n"))
	if got != "Go" {
		t.Errorf("DetectLanguageFromContent(main.go) = %q, want Go", got)
	}

	// Path-based detection takes precedence even when content looks like
	// a shebang (e.g. an .sh file that starts with `#!/bin/python` would
	// still be Bash because path takes precedence — and the extractor is
	// the one trusted with parsing).
	got = DetectLanguageFromContent("scripts/x.sh", []byte("#!/usr/bin/python\n"))
	if got != "Bash" {
		t.Errorf("DetectLanguageFromContent(.sh with python shebang) = %q, want Bash (path wins)", got)
	}
}

// First-line truncation: a 10MB binary blob without a newline must
// not allocate the full content into the shebang inspector. Pin the
// 512-byte cap so a future change that drops it surfaces in CI.
func TestDetectLanguageFromContent_BoundsFirstLineRead(t *testing.T) {
	// 10KB of garbage with no newline; first 2 bytes are #! but
	// nothing useful follows. Should NOT panic, should return "".
	garbage := make([]byte, 10*1024)
	garbage[0] = '#'
	garbage[1] = '!'
	for i := 2; i < len(garbage); i++ {
		garbage[i] = 'X'
	}
	if got := DetectLanguageFromContent("noisy.init", garbage); got != "" {
		t.Errorf("DetectLanguageFromContent(no-newline garbage) = %q, want \"\"", got)
	}
}

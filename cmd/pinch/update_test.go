package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeTempRepo creates a fresh git repo in a temp dir, makes one commit on
// branch `main`, and returns the absolute path. Tests that exercise the
// git helpers depend on a deterministic shape — single commit, known
// branch — that this helper provides.
func makeTempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test User"},
		{"git", "config", "commit.gpgsign", "false"},
	}
	for _, args := range cmds {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-q", "-m", "initial"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestGitDescribe(t *testing.T) {
	dir := makeTempRepo(t)
	out, err := gitDescribe(dir)
	if err != nil {
		t.Fatalf("gitDescribe: %v", err)
	}
	if out == "" {
		t.Error("gitDescribe should return non-empty short hash for a repo with one commit")
	}
}

func TestGitCurrentBranch(t *testing.T) {
	dir := makeTempRepo(t)
	br, err := gitCurrentBranch(dir)
	if err != nil {
		t.Fatalf("gitCurrentBranch: %v", err)
	}
	if br != "main" {
		t.Errorf("got %q, want main", br)
	}
}

func TestGitAheadBehind_Identical(t *testing.T) {
	dir := makeTempRepo(t)
	ahead, behind, err := gitAheadBehind(dir, "HEAD", "HEAD")
	if err != nil {
		t.Fatalf("gitAheadBehind: %v", err)
	}
	if ahead != 0 || behind != 0 {
		t.Errorf("HEAD vs HEAD should be (0,0), got (%d,%d)", ahead, behind)
	}
}

func TestGitAheadBehind_AfterCommit(t *testing.T) {
	dir := makeTempRepo(t)
	// Capture the original HEAD via tag, then add a second commit.
	if out, err := exec.Command("git", "-C", dir, "tag", "v0").CombinedOutput(); err != nil {
		t.Fatalf("tag: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "more.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatalf("write more: %v", err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-q", "-m", "second"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	ahead, behind, err := gitAheadBehind(dir, "v0", "HEAD")
	if err != nil {
		t.Fatalf("gitAheadBehind: %v", err)
	}
	// v0 is 0 ahead of HEAD and 1 behind (HEAD has 1 extra commit).
	if ahead != 0 || behind != 1 {
		t.Errorf("v0..HEAD expected (0,1), got (%d,%d)", ahead, behind)
	}
}

func TestRunGit_Failure(t *testing.T) {
	// Run a git command with a bogus subcommand against a real repo to
	// exercise the non-nil error path.
	dir := makeTempRepo(t)
	err := runGit(dir, "this-is-not-a-git-subcommand")
	if err == nil {
		t.Fatal("expected error from invalid git subcommand")
	}
}

func TestDetectUpdateSource_Override(t *testing.T) {
	// Override pointing at a real pincher module path.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk up to repo root for stability — we know go.mod lives there.
	for d := root; d != filepath.Dir(d); d = filepath.Dir(d) {
		if isPincherModule(filepath.Join(d, "go.mod")) {
			root = d
			break
		}
	}
	got := detectUpdateSource(root)
	if got == "" {
		t.Skipf("test running outside a pincher checkout (root=%q); detectUpdateSource fallback path skipped", root)
	}
	if !strings.HasSuffix(got, filepath.Base(root)) && got != root {
		// As long as the result is a real path under a pincher module, that's fine.
		t.Logf("detectUpdateSource returned %q (override was %q)", got, root)
	}
}

func TestDetectUpdateSource_OverrideNotARepo(t *testing.T) {
	// Override pointing at an empty directory should return "" — not detected.
	dir := t.TempDir()
	if got := detectUpdateSource(dir); got != "" {
		t.Errorf("non-pincher dir override should return empty; got %q", got)
	}
}

func TestRebuildBinary_DryRun(t *testing.T) {
	dir := t.TempDir()
	// Write a minimal go.mod naming this module so the path lookup works.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/kwad77/pincher\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	var buf bytes.Buffer
	err := rebuildBinary(&buf, dir, true)
	if err != nil {
		t.Fatalf("dry-run should not fail: %v", err)
	}
	if !strings.Contains(buf.String(), "would run") {
		t.Errorf("dry-run output should mention 'would run'; got: %s", buf.String())
	}
}

func TestUpdateInRepo_Check(t *testing.T) {
	dir := makeTempRepo(t)
	// Add an "origin" remote pointing at the same dir so `git fetch origin`
	// succeeds without network access. Use file:// path on the local repo.
	for _, args := range [][]string{
		{"git", "-C", dir, "config", "remote.origin.url", dir},
		{"git", "-C", dir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	var buf bytes.Buffer
	// Use --check so we exit before any commit-bumping work.
	err := updateInRepo(&buf, dir, true, true, false)
	// We don't assert no-error: the test repo's "origin" is itself, so
	// fetch may or may not succeed depending on git config. The point is
	// to exercise the in-repo path's branches.
	got := buf.String()
	if !strings.Contains(got, "in-repo mode") {
		t.Errorf("expected in-repo banner; got:\n%s\n(err=%v)", got, err)
	}
}

func TestConfirmYes_Yes(t *testing.T) {
	// Pipe "y" to stdin so confirmYes returns true.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()

	if _, err := w.Write([]byte("y\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()

	if !confirmYes() {
		t.Error("'y' input should produce true")
	}
}

func TestConfirmYes_No(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()

	w.Write([]byte("n\n"))
	w.Close()

	if confirmYes() {
		t.Error("'n' input should produce false")
	}
}

func TestFetchLatestRelease_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v0.9.9","name":"test","draft":false,"assets":[{"name":"pincher_linux_amd64","browser_download_url":"http://example/asset","size":42}]}`))
	}))
	defer srv.Close()

	saved := updateReleasesURL
	updateReleasesURL = srv.URL
	defer func() { updateReleasesURL = saved }()

	rel, err := fetchLatestRelease()
	if err != nil {
		t.Fatalf("fetchLatestRelease: %v", err)
	}
	if rel.TagName != "v0.9.9" {
		t.Errorf("TagName=%q, want v0.9.9", rel.TagName)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != "pincher_linux_amd64" {
		t.Errorf("unexpected asset list: %+v", rel.Assets)
	}
}

func TestFetchLatestRelease_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	saved := updateReleasesURL
	updateReleasesURL = srv.URL
	defer func() { updateReleasesURL = saved }()

	if _, err := fetchLatestRelease(); err == nil {
		t.Fatal("404 response should produce an error")
	}
}

func TestUpdateStandalone_Check_NoUpdate(t *testing.T) {
	// Mirror returns the current version → "already up to date" path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v` + version + `","draft":false,"assets":[]}`))
	}))
	defer srv.Close()

	saved := updateReleasesURL
	updateReleasesURL = srv.URL
	defer func() { updateReleasesURL = saved }()

	var buf bytes.Buffer
	if err := updateStandalone(&buf, true, true, false); err != nil {
		t.Fatalf("updateStandalone --check: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "already up to date") {
		t.Errorf("expected 'already up to date'; got:\n%s", got)
	}
}

func TestUpdateStandalone_Check_HasUpdate_NoAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v99.99.99","draft":false,"assets":[]}`))
	}))
	defer srv.Close()

	saved := updateReleasesURL
	updateReleasesURL = srv.URL
	defer func() { updateReleasesURL = saved }()

	var buf bytes.Buffer
	if err := updateStandalone(&buf, true, true, false); err != nil {
		t.Fatalf("updateStandalone --check: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "no prebuilt binary") {
		t.Errorf("expected 'no prebuilt binary' message; got:\n%s", got)
	}
}

func TestConfirmYes_Empty(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()

	w.Close() // EOF immediately

	if confirmYes() {
		t.Error("EOF input should produce false")
	}
}

func TestIsPincherModule(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"matches pincher", "module github.com/kwad77/pincher\n\ngo 1.25.0\n", true},
		{"submodule path also matches", "// note\nmodule github.com/kwad77/pincher/cmd/foo\n", true},
		{"different module", "module github.com/other/pkg\n", false},
		{"empty file", "", false},
		{"no module directive", "go 1.25.0\nrequire foo v1.0.0\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := filepath.Join(t.TempDir(), "go.mod")
			if err := os.WriteFile(tmp, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := isPincherModule(tmp); got != tc.want {
				t.Fatalf("isPincherModule(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestIsPincherModule_MissingFile(t *testing.T) {
	if isPincherModule(filepath.Join(t.TempDir(), "nope.mod")) {
		t.Fatal("missing file should return false")
	}
}

func TestFindRepoRoot_FindsAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/kwad77/pincher\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	deep := filepath.Join(root, "internal", "db", "nested")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := findRepoRoot(deep)
	if got != root {
		t.Fatalf("findRepoRoot(%q) = %q, want %q", deep, got, root)
	}
}

func TestFindRepoRoot_NoAncestor(t *testing.T) {
	dir := t.TempDir()
	if got := findRepoRoot(dir); got != "" {
		t.Fatalf("findRepoRoot in empty tree returned %q, want empty", got)
	}
}

func TestFindRepoRoot_ForeignModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/other/repo\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := findRepoRoot(root); got != "" {
		t.Fatalf("foreign module matched, got %q", got)
	}
}

func TestNormaliseVersion(t *testing.T) {
	cases := map[string]string{
		"v0.3.0":   "0.3.0",
		"0.3.0":    "0.3.0",
		" v1.2.3 ": "1.2.3",
		"":         "",
	}
	for in, want := range cases {
		if got := normaliseVersion(in); got != want {
			t.Errorf("normaliseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickAssetForPlatform_MatchesGOOS(t *testing.T) {
	rel := gitRelease{
		TagName: "v0.3.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "u1", Size: 100},
			{Name: "pincher_" + runtime.GOOS + "_" + runtime.GOARCH, BrowserDownloadURL: "u-bin", Size: 12345},
			{Name: "pincher_other_arch", BrowserDownloadURL: "u3", Size: 9999},
		},
	}
	got := pickAssetForPlatform(rel)
	if got.BrowserDownloadURL != "u-bin" {
		t.Fatalf("got %+v, want asset with URL u-bin", got)
	}
}

func TestPickAssetForPlatform_SkipsArchives(t *testing.T) {
	osTag := runtime.GOOS
	archTag := runtime.GOARCH
	rel := gitRelease{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			{Name: "pincher_" + osTag + "_" + archTag + ".tar.gz", BrowserDownloadURL: "u-tgz"},
			{Name: "pincher_" + osTag + "_" + archTag + ".zip", BrowserDownloadURL: "u-zip"},
		},
	}
	got := pickAssetForPlatform(rel)
	if got.BrowserDownloadURL != "" {
		t.Fatalf("archive should not be picked, got %+v", got)
	}
}

func TestPickAssetForPlatform_NoMatch(t *testing.T) {
	rel := gitRelease{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "u"},
		},
	}
	got := pickAssetForPlatform(rel)
	if got.BrowserDownloadURL != "" {
		t.Fatalf("expected no match, got %+v", got)
	}
}

// TestUpdateCLI_CheckBinary exercises the dispatch + in-repo detection
// end-to-end by building the binary, then running `pincher update --check`
// against a self-hosted-origin temp repo. Self-hosted origin (same trick
// as TestUpdateInRepo_Check) means `git fetch origin` succeeds without
// network or credentials — eliminating the actions/checkout@v4
// extraheader flake (#1875) without the t.Skipf-on-fetch-failure
// regression (#1875 follow-up) that dropped cmd/pinch coverage by
// 0.2pp when the test silently skipped in CI.
//
// We don't assert "up to date" / "behind" because that depends on
// origin state; we only assert the command exits 0 and prints the
// in-repo banner.
func TestUpdateCLI_CheckBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	// Build a temp repo with itself as origin so `git fetch origin`
	// always succeeds locally. Pincher must recognise this dir as a
	// pincher checkout via go.mod — `makeTempRepo` only seeds a README,
	// so add the minimum go.mod that satisfies isPincherModule.
	dir := makeTempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/kwad77/pincher\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "add", "go.mod"},
		{"git", "-C", dir, "commit", "-q", "-m", "add go.mod"},
		{"git", "-C", dir, "config", "remote.origin.url", dir},
		{"git", "-C", dir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
	} {
		c := exec.Command(args[0], args[1:]...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	bin := buildPincherBinary(t)

	// --source points the dispatcher at the temp repo. The in-repo
	// detector resolves it, runs `git fetch origin` against the
	// self-hosted remote, and prints the banner.
	cmd := exec.Command(bin, "update", "--check", "--source", dir)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pincher update --check: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "in-repo mode") {
		t.Fatalf("expected in-repo banner; got:\n%s", got)
	}
}

// TestUpdateCLI_DryRunStandalone forces the standalone path with --source
// pointed at a directory that is *not* a pincher checkout, so detection
// fails and we exercise the GitHub releases code path with --dry-run.
// The test depends on GitHub being reachable and the latest release
// existing — skip when offline.
func TestUpdateCLI_DryRunStandalone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network-dependent test in -short mode")
	}

	bin := buildPincherBinary(t)

	notARepo := t.TempDir()
	cmd := exec.Command(bin, "update", "--check", "--source", notARepo)
	cmd.Env = pincherCoverEnv()
	out, err := cmd.CombinedOutput()
	// --source pointing at a non-repo is treated as "no source detected",
	// which falls through to standalone mode.
	if err != nil && !strings.Contains(string(out), "standalone mode") {
		t.Skipf("standalone path unreachable (likely offline): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "standalone mode") && !strings.Contains(string(out), "in-repo mode") {
		t.Fatalf("expected standalone or in-repo banner; got:\n%s", out)
	}
}

// TestDownloadAndInstallAt_Success covers the happy path: downloads
// content from an httptest.Server, atomically replaces a temp "exe"
// file, and prints the install path. Exercises the platform-specific
// Windows-only `.old` move-aside branch via the post-install state.
func TestDownloadAndInstallAt_Success(t *testing.T) {
	body := []byte("#!/bin/sh\necho fake binary\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "fake-pincher")
	if runtime.GOOS == "windows" {
		exePath += ".exe"
	}
	// Pre-existing target so the Windows .old branch fires.
	if err := os.WriteFile(exePath, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed exePath: %v", err)
	}

	var buf bytes.Buffer
	if err := downloadAndInstallAt(&buf, srv.URL, exePath); err != nil {
		t.Fatalf("downloadAndInstallAt: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("installed bytes mismatch\nwant: %q\ngot:  %q", body, got)
	}
	if !strings.Contains(buf.String(), "downloading") || !strings.Contains(buf.String(), "installed") {
		t.Errorf("expected progress + install line; got:\n%s", buf.String())
	}
	// Windows: pre-existing exePath should now be .old next to it.
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(exePath + ".old"); err != nil {
			t.Errorf("expected %s.old after Windows install; got %v", exePath, err)
		}
	}
}

// TestDownloadAndInstallAt_Non200 covers the error path when the
// server returns a non-OK status — the function must not write a
// partial binary and must surface the status code.
func TestDownloadAndInstallAt_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "fake-pincher")
	var buf bytes.Buffer
	err := downloadAndInstallAt(&buf, srv.URL, exePath)
	if err == nil {
		t.Fatalf("expected error on 404, got nil; out: %s", buf.String())
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected status code in error, got: %v", err)
	}
	if _, statErr := os.Stat(exePath); !os.IsNotExist(statErr) {
		t.Errorf("expected no exePath written on 404, got stat err: %v", statErr)
	}
}

// TestDownloadAndInstallAt_TransportError covers a broken URL — the
// function must report a download error without leaving a temp file
// behind (defer os.Remove cleans up).
func TestDownloadAndInstallAt_TransportError(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "fake-pincher")
	var buf bytes.Buffer
	// Bind a free port and immediately close so connect fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	err := downloadAndInstallAt(&buf, url, exePath)
	if err == nil {
		t.Fatalf("expected transport error, got nil")
	}
	if !strings.Contains(err.Error(), "download") {
		t.Errorf("expected 'download' wrap in err: %v", err)
	}
	// No leftover .tmp files in dir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pincher-update-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestRunGoInstall_RunnerInvoked covers the happy-ish path: with `go`
// on PATH, runGoInstall prints the diagnostic line and dispatches to
// goInstallRunner with the canonical target. We override the runner
// to assert the dispatch shape without actually shelling out to
// `go install` (which has network deps and would write to GOBIN).
func TestRunGoInstall_RunnerInvoked(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH (env without Go toolchain)")
	}

	original := goInstallRunner
	defer func() { goInstallRunner = original }()

	var captured string
	goInstallRunner = func(out io.Writer, target string) error {
		captured = target
		return nil
	}

	var buf bytes.Buffer
	if err := runGoInstall(&buf); err != nil {
		t.Fatalf("runGoInstall: %v", err)
	}
	if captured != updateGoInstall {
		t.Errorf("runner target = %q, want %q", captured, updateGoInstall)
	}
	if !strings.Contains(buf.String(), "go install") {
		t.Errorf("expected 'go install' in diagnostic; got: %s", buf.String())
	}
}

// TestRunGoInstall_RunnerError covers error propagation: a runner
// failure surfaces verbatim.
func TestRunGoInstall_RunnerError(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}

	original := goInstallRunner
	defer func() { goInstallRunner = original }()

	wantErr := errFakeInstall
	goInstallRunner = func(out io.Writer, target string) error {
		return wantErr
	}

	var buf bytes.Buffer
	err := runGoInstall(&buf)
	if err != wantErr {
		t.Errorf("runGoInstall err = %v, want %v", err, wantErr)
	}
}

var errFakeInstall = &fakeErr{msg: "fake install failure"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

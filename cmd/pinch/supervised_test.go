package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSupervisedInnerBinary_DefaultsToProvider(t *testing.T) {
	gotPath, gotArgs, err := supervisedInnerBinary([]string{"--slow-query-ms", "100"}, "/stable/pincher", func(string) string {
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/stable/pincher" {
		t.Fatalf("path = %q, want provider path", gotPath)
	}
	if want := []string{"--slow-query-ms", "100"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %#v, want %#v", gotArgs, want)
	}
}

func TestSupervisedInnerBinary_EnvAndFlagOverride(t *testing.T) {
	gotPath, gotArgs, err := supervisedInnerBinary(
		[]string{"--inner-binary", "/dirty/flag-pincher", "--http", ":0"},
		"/stable/pincher",
		func(name string) string {
			if name == supervisedInnerBinaryEnv {
				return "/dirty/env-pincher"
			}
			return ""
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/dirty/flag-pincher" {
		t.Fatalf("path = %q, want flag override", gotPath)
	}
	if want := []string{"--http", ":0"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %#v, want %#v", gotArgs, want)
	}
}

func TestSupervisedInnerBinary_EqualsForm(t *testing.T) {
	gotPath, gotArgs, err := supervisedInnerBinary([]string{"--inner-binary=/dirty/pincher", "--verbose"}, "/stable/pincher", func(string) string {
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/dirty/pincher" {
		t.Fatalf("path = %q, want equals-form override", gotPath)
	}
	if want := []string{"--verbose"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %#v, want %#v", gotArgs, want)
	}
}

func TestSupervisedInnerBinary_MissingValue(t *testing.T) {
	if _, _, err := supervisedInnerBinary([]string{"--inner-binary"}, "/stable/pincher", func(string) string { return "" }); err == nil {
		t.Fatal("expected missing value error")
	}
}

func TestNormalizePincherVersionOutput(t *testing.T) {
	if got := normalizePincherVersionOutput("pincherMCP v0.94.0\n"); got != "0.94.0" {
		t.Fatalf("version = %q", got)
	}
}

func TestSameBinaryPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pincher")
	if !sameBinaryPath(path, filepath.Join(dir, ".", "pincher")) {
		t.Fatal("expected clean-equivalent paths to match")
	}
	if sameBinaryPath("", path) {
		t.Fatal("empty path should not match")
	}
	if sameBinaryPath(path, filepath.Join(dir, "other-pincher")) {
		t.Fatal("different paths should not match")
	}
}

func TestDetectPincherBinaryVersion(t *testing.T) {
	providerPath := filepath.Join(t.TempDir(), "pincher")
	if got := detectPincherBinaryVersion(providerPath, providerPath, "0.90.0"); got != "0.90.0" {
		t.Fatalf("provider version = %q", got)
	}
	if got := detectPincherBinaryVersion(filepath.Join(t.TempDir(), "missing-pincher"), providerPath, "0.90.0"); got != "" {
		t.Fatalf("missing action binary version = %q, want empty", got)
	}
}

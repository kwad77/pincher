// SPDX-License-Identifier: MIT

package server

import "testing"

// #733: fetch keyed the stored Document symbol on the raw input URL,
// so trivially-different spellings of the same resource
// (case-folded scheme/host, default :80/:443 port, missing trailing
// path, fragment) each produced a distinct symbol — duplicates piled
// up in search results. normalizeFetchURL collapses them.
func TestNormalizeFetchURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare host gets root path", "https://example.com", "https://example.com/"},
		{"trailing slash unchanged", "https://example.com/", "https://example.com/"},
		{"uppercase scheme", "HTTPS://example.com/", "https://example.com/"},
		{"uppercase host", "https://EXAMPLE.com/docs", "https://example.com/docs"},
		{"default https port stripped", "https://example.com:443/docs", "https://example.com/docs"},
		{"default http port stripped", "http://example.com:80/docs", "http://example.com/docs"},
		{"non-default port kept", "https://example.com:8443/docs", "https://example.com:8443/docs"},
		{"fragment dropped", "https://example.com/docs#section", "https://example.com/docs"},
		{"query preserved", "https://example.com/s?q=1", "https://example.com/s?q=1"},
		{"path case preserved", "https://example.com/Docs/API", "https://example.com/Docs/API"},
		{"unparseable returned as-is", "://not a url", "://not a url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeFetchURL(c.in); got != c.want {
				t.Errorf("normalizeFetchURL(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

// Spellings that differ only in normalizable noise must collapse to
// the same stored symbol ID.
func TestNormalizeFetchURL_CollapsesEquivalentSpellings(t *testing.T) {
	t.Parallel()
	equivalent := []string{
		"https://example.com",
		"https://example.com/",
		"https://EXAMPLE.com/",
		"HTTPS://example.com:443/",
		"https://example.com/#top",
	}
	want := normalizeFetchURL(equivalent[0])
	for _, u := range equivalent[1:] {
		if got := normalizeFetchURL(u); got != want {
			t.Errorf("normalizeFetchURL(%q) = %q; want %q (must collapse with %q)",
				u, got, want, equivalent[0])
		}
	}
}

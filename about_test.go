package main

import "testing"

func TestAllowedURL(t *testing.T) {
	for u, want := range map[string]bool{
		repoURL:                          true,
		releasesURL:                      true,
		repoURL + "/issues":              true,
		repoURL + ".evil.example":        false,
		"https://example.com":            false,
		"file:///etc/passwd":             false,
		"https://github.com/jm23-23/Enk": false,
	} {
		if got := allowedURL(u); got != want {
			t.Errorf("allowedURL(%q) = %v, want %v", u, got, want)
		}
	}
}

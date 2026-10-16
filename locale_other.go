//go:build !windows

package main

import "os"

// systemLang reads the POSIX locale variables in their usual priority.
func systemLang() string {
	for _, k := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			return v
		}
	}
	return langEN
}

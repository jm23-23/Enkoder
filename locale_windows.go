//go:build windows

package main

import "golang.org/x/sys/windows"

// systemLang returns the user's preferred Windows UI language, e.g. "pl-PL".
func systemLang() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return langEN
	}
	return langs[0]
}

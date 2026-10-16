package main

import "strings"

// version is shown in the window title and the UI header. It is a var so
// release builds can override it: go build -ldflags "-X main.version=1.2.3".
var version = "0.6.0"

func appTitle() string {
	return "Enkoder " + version
}

// uiHTML fills the {{VERSION}} and {{LANG}} placeholders in the embedded page.
func uiHTML() string {
	return strings.NewReplacer("{{VERSION}}", version, "{{LANG}}", lang()).Replace(indexHTML)
}

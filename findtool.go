package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, c := range extraToolCandidates(name) {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func extraToolCandidates(name string) []string {
	if runtime.GOOS == "windows" {
		exe := name + ".exe"
		return []string{
			filepath.Join(`c:\ffmpeg\bin`, exe),
			filepath.Join(`C:\ffmpeg\bin`, exe),
		}
	}
	return []string{
		filepath.Join("/usr/bin", name),
		filepath.Join("/usr/local/bin", name),
	}
}

func missingToolsMessage() string {
	if runtime.GOOS == "windows" {
		return tr("missing_tools_win")
	}
	return tr("missing_tools")
}

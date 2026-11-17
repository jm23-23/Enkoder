package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// Where the ffmpeg in use came from.
const (
	toolSourceSetting = "setting" // the "Custom ffmpeg path" setting
	toolSourceEnv     = "env"     // ENKODER_FFMPEG
	toolSourceAuto    = "auto"    // PATH, then the usual install folders
)

// toolChoice is the resolved ffmpeg / ffprobe pair.
type toolChoice struct {
	FFmpeg  string
	FFprobe string
	Source  string
	// Problem is set when the configured path (setting or env) could not be
	// used and the search fell back to the next option.
	Problem string
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// customTools turns a user-given path into ffmpeg / ffprobe paths. The path
// may name the ffmpeg binary or the folder that holds it; ffprobe is taken
// from the same folder and is "" when it is not there.
func customTools(p string) (ffmpegPath, ffprobePath string, err error) {
	p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"`))
	if p == "" {
		return "", "", errors.New(tr("ffpath_empty"))
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, herr := os.UserHomeDir(); herr == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		return "", "", errors.New(tr("ffpath_not_absolute", p))
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", "", errors.New(tr("ffpath_not_found", p))
	}
	if st.IsDir() {
		p = filepath.Join(p, exeName("ffmpeg"))
		if st, err = os.Stat(p); err != nil || st.IsDir() {
			return "", "", errors.New(tr("ffpath_no_ffmpeg_in_dir", filepath.Dir(p)))
		}
	}
	if !isExecutable(st) {
		return "", "", errors.New(tr("ffpath_not_executable", p))
	}
	probe := filepath.Join(filepath.Dir(p), exeName("ffprobe"))
	if pst, perr := os.Stat(probe); perr != nil || pst.IsDir() || !isExecutable(pst) {
		probe = ""
	}
	return p, probe, nil
}

func isExecutable(st os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
}

// resolveTools picks ffmpeg and ffprobe: the saved setting first, then the
// ENKODER_FFMPEG / ENKODER_FFPROBE environment variables, then findTool. A
// configured path that does not work is reported in Problem and skipped, so
// a stale setting never locks the user out.
func resolveTools(setting string) toolChoice {
	var problems []string
	try := func(p, source string) (toolChoice, bool) {
		ff, fp, err := customTools(p)
		if err != nil {
			problems = append(problems, err.Error())
			return toolChoice{}, false
		}
		if env := strings.TrimSpace(os.Getenv("ENKODER_FFPROBE")); env != "" && source == toolSourceEnv {
			fp = env
		}
		if fp == "" {
			fp = findTool("ffprobe")
		}
		return toolChoice{FFmpeg: ff, FFprobe: fp, Source: source}, true
	}

	var c toolChoice
	ok := false
	if strings.TrimSpace(setting) != "" {
		c, ok = try(setting, toolSourceSetting)
	}
	if !ok {
		if env := strings.TrimSpace(os.Getenv("ENKODER_FFMPEG")); env != "" {
			c, ok = try(env, toolSourceEnv)
		}
	}
	if !ok {
		c = toolChoice{FFmpeg: findTool("ffmpeg"), FFprobe: findTool("ffprobe"), Source: toolSourceAuto}
	}
	c.Problem = strings.Join(problems, "\n")
	return c
}

// startupToolsError is the message shown when no usable ffmpeg was found.
func startupToolsError(c toolChoice) string {
	msg := missingToolsMessage()
	if c.Problem != "" {
		msg = fmt.Sprintf("%s\n\n%s", c.Problem, msg)
	}
	return msg
}

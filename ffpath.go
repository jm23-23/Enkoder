package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"enkoder/internal/ffmpeg"
	"enkoder/internal/quality"
)

// The "Custom ffmpeg path" setting. Capabilities (NVENC, CUDA, metric, …)
// are detected once per process, so a new path takes effect after a restart;
// the settings window offers one.

// ffmpegCheckDTO describes a candidate ffmpeg, shown before it is saved.
type ffmpegCheckDTO struct {
	Input   string `json:"input"`
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	FFmpeg  string `json:"ffmpeg"`
	FFprobe string `json:"ffprobe"`
	Version string `json:"version"`
	Metric  string `json:"metric"`
	NVENC   bool   `json:"nvenc"`
	SVTAV1  bool   `json:"svtav1"`
}

// ffmpegSettingsDTO is the current state for the settings window.
type ffmpegSettingsDTO struct {
	Setting string `json:"setting"` // saved value, "" = automatic
	FFmpeg  string `json:"ffmpeg"`  // in use now
	FFprobe string `json:"ffprobe"`
	Source  string `json:"source"` // setting, env, auto
	Problem string `json:"problem"`
	// Pending is true when the saved setting differs from what this process
	// started with, i.e. a restart is needed.
	Pending bool `json:"pending"`
}

// inspectFFmpeg checks that path is a working ffmpeg and lists what it can do.
func inspectFFmpeg(path string) ffmpegCheckDTO {
	d := ffmpegCheckDTO{Input: path}
	ff, fp, err := customTools(path)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	d.FFmpeg, d.FFprobe = ff, fp
	if d.FFprobe == "" {
		d.FFprobe = findTool("ffprobe")
	}
	d.Version = ffmpeg.ToolVersion(ff)
	if d.Version == "" {
		cmd := exec.Command(ff, "-hide_banner", "-version")
		_, runErr := cmd.Output()
		if runErr == nil {
			runErr = errors.New("no version line")
		}
		d.Error = tr("ffpath_not_ffmpeg", ff, runErr)
		return d
	}
	d.Metric = string(quality.DetectMetric(ff))
	d.NVENC = ffmpeg.HasEncoder(ff, "hevc_nvenc")
	d.SVTAV1 = ffmpeg.HasEncoder(ff, "libsvtav1")
	d.OK = true
	return d
}

func (a *app) ffmpegSettings() ffmpegSettingsDTO {
	s, _ := loadSettings()
	return ffmpegSettingsDTO{
		Setting: s.FFmpegPath,
		FFmpeg:  a.ffmpegPath,
		FFprobe: a.ffprobePath,
		Source:  a.toolSource,
		Problem: a.toolProblem,
		Pending: s.FFmpegPath != a.startSetting,
	}
}

// pushJS calls a page function with v as its JSON argument, if it exists.
func (a *app) pushJS(fn string, v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}
	js := fmt.Sprintf(`(function(){ if (typeof %s === "function") %s(%s); })()`, fn, fn, payload)
	a.view.Dispatch(func() { a.view.Eval(js) })
}

// Bound to the page. Results come back with Eval (applyFFmpegSettings,
// applyFFmpegCheck), because on Linux Bind promises may never resolve.

func (a *app) getFFmpegSettings() error {
	go a.pushJS("applyFFmpegSettings", a.ffmpegSettings())
	return nil
}

func (a *app) checkFFmpeg(path string) error {
	go a.pushJS("applyFFmpegCheck", inspectFFmpeg(path))
	return nil
}

func (a *app) pickFFmpeg() error {
	go func() {
		path, err := pickExecutable()
		if err != nil {
			a.emitEvent("enkoder-log", err.Error())
			return
		}
		if path == "" {
			return
		}
		a.pushJS("applyFFmpegPicked", path)
		a.pushJS("applyFFmpegCheck", inspectFFmpeg(path))
	}()
	return nil
}

// saveFFmpegPath stores the setting; "" goes back to automatic search.
// A non-empty path is saved only when it runs as ffmpeg.
func (a *app) saveFFmpegPath(path string) error {
	go func() {
		if path != "" {
			d := inspectFFmpeg(path)
			if !d.OK {
				a.pushJS("applyFFmpegCheck", d)
				return
			}
			path = d.FFmpeg // stored as the binary, with ~ expanded
		}
		if err := updateSettings(func(s *settings) { s.FFmpegPath = path }); err != nil {
			a.pushJS("applyFFmpegCheck", ffmpegCheckDTO{Input: path, Error: err.Error()})
			return
		}
		a.pushJS("applyFFmpegSettings", a.ffmpegSettings())
	}()
	return nil
}

// restartApp starts a new Enkoder and quits this one, so a changed ffmpeg
// path takes effect. Refused while an encode runs.
func (a *app) restartApp() error {
	a.mu.Lock()
	busy := a.busy
	a.mu.Unlock()
	if busy {
		a.emitEvent("enkoder-log", tr("ffpath_busy"))
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		a.emitEvent("enkoder-log", err.Error())
		return nil
	}
	if p, err := filepath.EvalSymlinks(self); err == nil {
		self = p
	}
	cmd := exec.Command(self, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		a.emitEvent("enkoder-log", err.Error())
		return nil
	}
	go func() {
		// Let the bound call return before the process goes away.
		time.Sleep(150 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

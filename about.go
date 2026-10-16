package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"enkoder/internal/ffmpeg"
)

// Links the About window may open. openURL refuses anything else, so the
// page cannot be used to launch arbitrary programs or addresses.
const (
	repoURL     = "https://github.com/jm23-23/Enkoder"
	releasesURL = repoURL + "/releases"
)

type aboutDTO struct {
	Version        string `json:"version"`
	OS             string `json:"os"`
	FFmpegPath     string `json:"ffmpegPath"`
	FFmpegVersion  string `json:"ffmpegVersion"`
	FFprobePath    string `json:"ffprobePath"`
	FFprobeVersion string `json:"ffprobeVersion"`
	NVENC          bool   `json:"nvenc"`
	NVENCTuned     bool   `json:"nvencTuned"`
	CUDA           bool   `json:"cuda"`
	SVTAV1         bool   `json:"svtav1"`
	AVX512         bool   `json:"avx512"`
}

var (
	aboutOnce sync.Once
	aboutInfo aboutDTO
)

// aboutData collects the environment once; the GPU probes are cached by
// the ffmpeg package and already warmed at startup.
func (a *app) aboutData() aboutDTO {
	aboutOnce.Do(func() {
		aboutInfo = aboutDTO{
			Version:        version,
			OS:             runtime.GOOS + "/" + runtime.GOARCH,
			FFmpegPath:     a.ffmpegPath,
			FFmpegVersion:  ffmpeg.ToolVersion(a.ffmpegPath),
			FFprobePath:    a.ffprobePath,
			FFprobeVersion: ffmpeg.ToolVersion(a.ffprobePath),
			NVENC:          a.nvenc,
			NVENCTuned:     a.nvenc && ffmpeg.HasNVENCTuned(a.ffmpegPath),
			CUDA:           a.nvenc && ffmpeg.HasCUDA(a.ffmpegPath),
			SVTAV1:         ffmpeg.HasSVTAV1(a.ffmpegPath),
			AVX512:         hasAVX512(),
		}
	})
	return aboutInfo
}

// about is bound to the page. The result is pushed with Eval (applyAbout)
// because on Linux Bind promises may never resolve.
func (a *app) about() error {
	go func() {
		payload, err := json.Marshal(a.aboutData())
		if err != nil {
			return
		}
		js := fmt.Sprintf(`(function(){ if (typeof applyAbout === "function") applyAbout(%s); })()`, payload)
		a.view.Dispatch(func() { a.view.Eval(js) })
	}()
	return nil
}

func allowedURL(u string) bool {
	return u == repoURL || strings.HasPrefix(u, repoURL+"/")
}

// openURL opens a project link in the default browser.
func (a *app) openURL(u string) error {
	if !allowedURL(u) {
		return errors.New("URL not allowed")
	}
	return openBrowser(u)
}

// copyText puts text on the system clipboard (the page has no reliable
// clipboard access in WebKitGTK / WebView2 when loaded from a string).
// Bind callbacks may run on the UI thread, so it must not wait on Dispatch.
func (a *app) copyText(text string) error {
	a.view.Dispatch(func() { _ = setClipboard(text) })
	return nil
}

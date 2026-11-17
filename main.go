package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"enkoder/internal/ffmpeg"
	"enkoder/internal/ffprobe"
	"enkoder/internal/quality"
	"enkoder/internal/suggest"

	"github.com/ncruces/zenity"
	"golang.org/x/sys/cpu"
)

//go:embed ui/index.html
var indexHTML string

type app struct {
	ffmpegPath   string
	ffprobePath  string
	toolSource   string // toolSourceSetting / Env / Auto
	toolProblem  string // why the configured path was skipped, or ""
	startSetting string // the ffmpeg path setting this process started with
	nvenc        bool
	defaultCodec string
	view         webviewHost

	mu     sync.Mutex
	cancel context.CancelFunc
	cmd    *exec.Cmd
	busy   bool

	gpuFilterMu    sync.Mutex
	gpuFilterCache map[string]bool

	metricOnce sync.Once
	metric     quality.Metric
}

type streamDTO struct {
	Index       int            `json:"index"`
	CodecType   string         `json:"codec_type"`
	Label       string         `json:"label"`
	Disposition map[string]int `json:"disposition"`
}

type probeDTO struct {
	Streams         []streamDTO `json:"streams"`
	SuggestedOutput string      `json:"suggestedOutput"`
	DurationSeconds float64     `json:"durationSeconds"`
	SuggestedCQ     int         `json:"suggestedCQ"`
	SuggestedPixFmt string      `json:"suggestedPixFmt"`
	SuggestedPreset string      `json:"suggestedPreset"`
	HDR             bool        `json:"hdr"`
	DolbyVision     bool        `json:"dolbyVision"`
	Interlaced      bool        `json:"interlaced"`
	BitsPerSample   int         `json:"bitsPerSample"`
	Width           int         `json:"width"`
	Height          int         `json:"height"`
	ColorTransfer   string      `json:"colorTransfer"`
	ColorPrimaries  string      `json:"colorPrimaries"`
	ColorSpace      string      `json:"colorSpace"`
	SuggestedAV1CQ  int         `json:"suggestedAV1CQ"`
	HDR10           *hdr10DTO   `json:"hdr10"`
}

// hdr10DTO is the source's static HDR10 metadata, shown in the UI and sent
// back with the job so x265 / SVT-AV1 get it explicitly.
type hdr10DTO struct {
	MasterDisplay string  `json:"masterDisplay"`
	MaxCLL        string  `json:"maxCll"`
	MaxLuminance  float64 `json:"maxLuminance"`
	MinLuminance  float64 `json:"minLuminance"`
	HasCLL        bool    `json:"hasCll"`
	CLL           int     `json:"cll"`
	FALL          int     `json:"fall"`
	HDR10Plus     bool    `json:"hdr10Plus"`
	Problem       string  `json:"problem"`
}

type capsDTO struct {
	NVENC        bool   `json:"nvenc"`
	SVTAV1       bool   `json:"svtav1"`
	DefaultCodec string `json:"defaultCodec"`
	// QualityMetric is the metric "Find CRF" uses (vmaf, xpsnr, ssim) or "".
	QualityMetric string `json:"qualityMetric"`
}

// qualityDTO is the result of a target-quality search, sent to the page.
type qualityDTO struct {
	CRF            int     `json:"crf"`
	Score          float64 `json:"score"`
	Target         float64 `json:"target"`
	Metric         string  `json:"metric"`
	Met            bool    `json:"met"`
	EstimatedBytes int64   `json:"estimatedBytes"`
	Trials         int     `json:"trials"`
}

type jobDTO struct {
	Input     string `json:"input"`
	Output    string `json:"output"`
	Video     []int  `json:"video"`
	Audio     []int  `json:"audio"`
	Subtitle  []int  `json:"subtitle"`
	Codec     string `json:"codec"`
	Container string `json:"container"`
	// CQ is a pointer so JSON null/missing defaults to 18, while explicit 0 is kept.
	CQ              *int    `json:"cq"`
	Preset          string  `json:"preset"`
	Tune            string  `json:"tune"`
	Speed           string  `json:"speed"`
	PixFmt          string  `json:"pixFmt"`
	Deinterlace     bool    `json:"deinterlace"`
	HDR             bool    `json:"hdr"`
	ColorTransfer   string  `json:"colorTransfer"`
	ColorPrimaries  string  `json:"colorPrimaries"`
	ColorSpace      string  `json:"colorSpace"`
	ScaleHeight     int     `json:"scaleHeight"`
	MasterDisplay   string  `json:"masterDisplay"`
	MaxCLL          string  `json:"maxCll"`
	DurationSeconds float64 `json:"durationSeconds"`
}

func main() {
	initLang()
	saved, _ := loadSettings()
	tools := resolveTools(saved.FFmpegPath)
	ffmpegPath, ffprobePath := tools.FFmpeg, tools.FFprobe
	if ffmpegPath == "" || ffprobePath == "" {
		zenity.Error(startupToolsError(tools), zenity.Title(appTitle()))
		os.Exit(1)
	}

	nvenc := ffmpeg.HasHEVCNVENC(ffmpegPath)
	defaultCodec := string(ffmpeg.DefaultVideoCodec(ffmpegPath))

	w, err := newHost()
	if err != nil {
		zenity.Error(err.Error(), zenity.Title(appTitle()))
		os.Exit(1)
	}
	defer w.Destroy()

	a := &app{
		ffmpegPath:   ffmpegPath,
		ffprobePath:  ffprobePath,
		toolSource:   tools.Source,
		toolProblem:  tools.Problem,
		startSetting: saved.FFmpegPath,
		nvenc:        nvenc,
		defaultCodec: defaultCodec,
		view:         w,
	}
	a.warmHardwareProbes()
	a.bind()
	w.SetSize(920, 780)
	w.LoadUI(uiHTML())
	a.pushInitialCaps()
	if tools.Problem != "" {
		go func() {
			time.Sleep(500 * time.Millisecond) // after the page has loaded
			a.emitEvent("enkoder-log", tr("ffpath_fallback", tools.Problem, ffmpegPath))
		}()
	}
	a.startStatsPump()
	w.Run()
}

func (a *app) bind() {
	must(a.view.Bind("openInput", a.openInput))
	must(a.view.Bind("saveOutput", a.saveOutput))
	must(a.view.Bind("probe", a.probe))
	must(a.view.Bind("suggestName", a.suggestName))
	must(a.view.Bind("preview", a.preview))
	must(a.view.Bind("encode", a.encode))
	must(a.view.Bind("findQuality", a.findQuality))
	must(a.view.Bind("cancel", a.cancelEncode))
	must(a.view.Bind("capabilities", a.capabilities))
	must(a.view.Bind("systemStats", a.systemStats))
	must(a.view.Bind("setLanguage", a.setLanguage))
	must(a.view.Bind("about", a.about))
	must(a.view.Bind("openURL", a.openURL))
	must(a.view.Bind("copyText", a.copyText))
	must(a.view.Bind("getFFmpegSettings", a.getFFmpegSettings))
	must(a.view.Bind("checkFFmpeg", a.checkFFmpeg))
	must(a.view.Bind("pickFFmpeg", a.pickFFmpeg))
	must(a.view.Bind("saveFFmpegPath", a.saveFFmpegPath))
	must(a.view.Bind("restartApp", a.restartApp))

	// Bridge: HTML expects window.enkoder.*; Go Bind exposes top-level functions.
	a.view.Init(`
(function () {
  window.enkoder = {
    openInput: function () { return window.openInput.apply(null, arguments); },
    saveOutput: function () { return window.saveOutput.apply(null, arguments); },
    probe: function () { return window.probe.apply(null, arguments); },
    suggestName: function () { return window.suggestName.apply(null, arguments); },
    preview: function () { return window.preview.apply(null, arguments); },
    encode: function () { return window.encode.apply(null, arguments); },
    findQuality: function () { return window.findQuality.apply(null, arguments); },
    cancel: function () { return window.cancel.apply(null, arguments); },
    capabilities: function () { return window.capabilities.apply(null, arguments); },
    systemStats: function () { return window.systemStats.apply(null, arguments); },
    setLanguage: function () { return window.setLanguage.apply(null, arguments); },
    about: function () { return window.about.apply(null, arguments); },
    openURL: function () { return window.openURL.apply(null, arguments); },
    copyText: function () { return window.copyText.apply(null, arguments); },
    getFFmpegSettings: function () { return window.getFFmpegSettings.apply(null, arguments); },
    checkFFmpeg: function () { return window.checkFFmpeg.apply(null, arguments); },
    pickFFmpeg: function () { return window.pickFFmpeg.apply(null, arguments); },
    saveFFmpegPath: function () { return window.saveFFmpegPath.apply(null, arguments); },
    restartApp: function () { return window.restartApp.apply(null, arguments); },
    onLog: function (cb) {
      if (typeof cb !== "function") return;
      window.addEventListener("enkoder-log", function (e) { cb(e.detail); });
    }
  };
})();
`)
	wrapFilePickerBinds(a.view)
}

// warmHardwareProbes runs the slower GPU probes (~0.4 s each) in the
// background so the window opens immediately; jobToOptions waits on them.
func (a *app) warmHardwareProbes() {
	if !a.nvenc {
		return
	}
	go ffmpeg.HasCUDA(a.ffmpegPath)
	go ffmpeg.HasNVENCTuned(a.ffmpegPath)
}

// hasAVX512 matches the AVX-512 subsets x265's avx512 kernels need.
func hasAVX512() bool {
	return cpu.X86.HasAVX512F && cpu.X86.HasAVX512BW
}

func (a *app) capabilities() (capsDTO, error) {
	return capsDTO{
		NVENC:         a.nvenc,
		SVTAV1:        ffmpeg.HasSVTAV1(a.ffmpegPath),
		DefaultCodec:  a.defaultCodec,
		QualityMetric: string(a.qualityMetric()),
	}, nil
}

// qualityMetric is the best quality metric this ffmpeg has. Cached.
func (a *app) qualityMetric() quality.Metric {
	a.metricOnce.Do(func() {
		a.metric = quality.DetectMetric(a.ffmpegPath)
	})
	return a.metric
}

func (a *app) selectInputFile() (string, error) {
	if p := strings.TrimSpace(os.Getenv("ENKODER_FAKE_PICK")); p != "" {
		return normalizePickedPath(p), nil
	}
	path, err := pickOpenFile()
	if err != nil {
		return "", err
	}
	return normalizePickedPath(path), nil
}

func (a *app) selectOutputFile(suggested, container string) (string, error) {
	filename := filepath.Base(suggested)
	if filename == "." || filename == "" {
		filename = "output." + strings.TrimPrefix(container, ".")
		if container == "" {
			filename = "output.mkv"
		}
	}
	dir := filepath.Dir(suggested)
	if dir == "." || suggested == "" {
		dir = ""
	}
	ext := suggestExt(container)
	path, err := pickSaveFile(filepath.Join(dir, filename), ext)
	if err != nil {
		return "", err
	}
	return normalizePickedPath(path), nil
}

// normalizePickedPath trims zenity output and converts file:// URIs to paths.
func normalizePickedPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"`)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(path), "file:") {
		if u, err := url.Parse(path); err == nil && u.Scheme == "file" && u.Path != "" {
			return u.Path
		}
	}
	return path
}

func (a *app) probe(input string) (probeDTO, error) {
	res, err := ffprobe.Probe(a.ffprobePath, input)
	if err != nil {
		return probeDTO{}, err
	}
	vq := ffprobe.FirstVideoQuality(res)
	out := probeDTO{
		SuggestedOutput: suggest.OutputName(input, "mkv", suggest.TagHEVC, 0),
		Streams:         make([]streamDTO, 0, len(res.Streams)),
		DurationSeconds: ffprobe.DurationSeconds(res),
		SuggestedCQ:     ffmpeg.SuggestedCRF(vq.Width, vq.Height),
		SuggestedAV1CQ:  ffmpeg.SuggestedAV1CRF(vq.Width, vq.Height),
		SuggestedPixFmt: ffmpeg.SuggestedPixFmt(vq.HDR, vq.BitsPerSample),
		SuggestedPreset: ffmpeg.DefaultPreset(ffmpeg.CodecLibX265),
		HDR:             vq.HDR,
		DolbyVision:     vq.DolbyVision,
		Interlaced:      vq.Interlaced,
		BitsPerSample:   vq.BitsPerSample,
		Width:           vq.Width,
		Height:          vq.Height,
		ColorTransfer:   vq.ColorTransfer,
		ColorPrimaries:  vq.ColorPrimaries,
		ColorSpace:      vq.ColorSpace,
	}
	if vq.HDR && strings.EqualFold(vq.ColorTransfer, "smpte2084") {
		if info, err := ffprobe.ProbeHDR10(a.ffprobePath, input); err == nil {
			out.HDR10 = &hdr10DTO{
				MasterDisplay: info.MasterDisplay,
				MaxLuminance:  info.MaxLuminance,
				MinLuminance:  info.MinLuminance,
				HasCLL:        info.HasCLL,
				CLL:           info.MaxCLL,
				FALL:          info.MaxFALL,
				HDR10Plus:     info.HDR10Plus,
				Problem:       info.Problem,
			}
			// 0,0 means "unknown"; leave it to the encoder default.
			if info.HasCLL && (info.MaxCLL > 0 || info.MaxFALL > 0) {
				out.HDR10.MaxCLL = fmt.Sprintf("%d,%d", info.MaxCLL, info.MaxFALL)
			}
		}
	}
	for _, s := range res.Streams {
		out.Streams = append(out.Streams, streamDTO{
			Index:       s.Index,
			CodecType:   s.CodecType,
			Label:       s.Label(),
			Disposition: s.Disposition,
		})
	}
	return out, nil
}

func (a *app) suggestName(input, container, codec string, scaleHeight float64) (string, error) {
	tag := suggest.TagNone
	switch ffmpeg.VideoCodec(codec) {
	case ffmpeg.CodecHEVCNVENC, ffmpeg.CodecLibX265:
		tag = suggest.TagHEVC
	case ffmpeg.CodecSVTAV1:
		tag = suggest.TagAV1
	}
	return suggest.OutputName(input, container, tag, int(scaleHeight)), nil
}

func (a *app) preview(job jobDTO) (string, error) {
	opt, err := a.jobToOptions(job)
	if err != nil {
		return "", err
	}
	return ffmpeg.CommandPreview(a.ffmpegPath, opt), nil
}

// encode validates and starts ffmpeg in a background goroutine, then returns
// immediately so the UI thread stays responsive. Completion is signaled
// via CustomEvent enkoder-done / enkoder-error / enkoder-cancelled.
func (a *app) encode(job jobDTO) error {
	opt, err := a.jobToOptions(job)
	if err != nil {
		return err
	}
	a.mu.Lock()
	busy := a.busy
	a.mu.Unlock()
	if busy {
		return errors.New(tr("busy"))
	}
	// ffmpeg dostaje -y, więc o nadpisanie istniejącego pliku pytamy tutaj.
	// Dialog blokuje, dlatego w gorutynie; wynik idzie zdarzeniem, bo na
	// Linuksie obietnice z Bind mogą się nigdy nie rozwiązać.
	if fileExists(opt.Output) {
		go func() {
			if !confirmOverwrite(opt.Output) {
				a.emitEvent("enkoder-not-started", "")
				return
			}
			if err := a.startEncode(job, opt); err != nil {
				a.emitEvent("enkoder-error", err.Error())
			}
		}()
		return nil
	}
	return a.startEncode(job, opt)
}

// startEncode runs a validated job; the caller has already dealt with an
// existing output file.
func (a *app) startEncode(job jobDTO, opt ffmpeg.Options) error {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return errors.New(tr("busy"))
	}
	if a.cancel != nil {
		a.cancel()
	}
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Stan pliku wynikowego przed startem: po błędzie usuwamy tylko to,
	// co ffmpeg faktycznie utworzył lub nadpisał.
	before, _ := os.Stat(opt.Output)
	a.cancel = cancel
	a.cmd = nil
	a.busy = true
	a.mu.Unlock()

	preview := ffmpeg.CommandPreview(a.ffmpegPath, opt)
	a.emitEvent("enkoder-log", preview)

	duration := job.DurationSeconds
	if duration <= 0 {
		if d := a.probeDuration(job.Input); d > 0 {
			duration = d
		}
	}
	a.emitProgress(0, 0, duration)

	logCh := make(chan string, 128)
	var logWG sync.WaitGroup
	logWG.Add(1)
	go func() {
		defer logWG.Done()
		a.pumpLogs(logCh)
	}()

	go func() {
		defer cancel()

		var (
			durMu         sync.Mutex
			dur           = duration
			lastEmit      time.Time
			progressEvery = 120 * time.Millisecond
			pos           ffmpeg.PositionTracker
		)

		runErr := ffmpeg.Run(ctx, a.ffmpegPath, opt, ffmpeg.RunOptions{
			OnStart: func(cmd *exec.Cmd) {
				a.mu.Lock()
				a.cmd = cmd
				a.mu.Unlock()
			},
			OnLine: func(line string) {
				durMu.Lock()
				if dur <= 0 {
					if d, ok := ffmpeg.ParseInputDuration(line); ok && d > 0 {
						dur = d
					}
				}
				curDur := dur
				durMu.Unlock()

				if sec, ok := pos.Position(line); ok {
					pct := ffmpeg.ProgressPercent(sec, curDur)
					now := time.Now()
					durMu.Lock()
					emit := lastEmit.IsZero() || now.Sub(lastEmit) >= progressEvery || pct >= 100
					if emit {
						lastEmit = now
					}
					durMu.Unlock()
					if emit {
						a.emitProgress(pct, sec, curDur)
					}
				}
				select {
				case logCh <- line:
				default:
					// drop if UI is behind — keep encode responsive
				}
			},
		})

		close(logCh)
		logWG.Wait()

		a.mu.Lock()
		a.busy = false
		a.cancel = nil
		a.cmd = nil
		a.mu.Unlock()

		durMu.Lock()
		finalDur := dur
		durMu.Unlock()

		if runErr != nil {
			removed, err := removePartialOutput(opt.Output, before)
			switch {
			case err != nil:
				a.emitEvent("enkoder-log", tr("partial_remove_failed", opt.Output, err))
			case removed:
				a.emitEvent("enkoder-log", tr("partial_removed", opt.Output))
			}
		}

		switch {
		case runErr == nil:
			a.emitProgress(100, finalDur, finalDur)
			a.emitEvent("enkoder-done", "")
		case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
			a.emitEvent("enkoder-cancelled", "")
		default:
			a.emitEvent("enkoder-error", runErr.Error())
		}
	}()

	return nil
}

// findQuality searches for the highest CRF / CQ that meets the quality
// level on samples of the source, with the job's codec and settings. Like
// encode it runs in the background, can be cancelled, and reports through
// events: enkoder-quality-progress, enkoder-log, then enkoder-quality (the
// result), enkoder-cancelled or enkoder-error.
func (a *app) findQuality(job jobDTO, level string) error {
	opt, err := a.jobToOptions(job)
	if err != nil {
		return err
	}
	if opt.VideoCodec == ffmpeg.CodecCopy {
		return errors.New(tr("quality_needs_enc"))
	}
	metric := a.qualityMetric()
	if metric == "" {
		return errors.New(tr("no_metric"))
	}
	target, ok := quality.Target(metric, quality.Level(level))
	if !ok {
		return errors.New(tr("bad_level", level))
	}
	duration := job.DurationSeconds
	if duration <= 0 {
		duration = a.probeDuration(job.Input)
	}
	if duration <= 0 {
		return errors.New(tr("no_duration"))
	}

	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return errors.New(tr("busy"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.cmd = nil
	a.busy = true
	a.mu.Unlock()

	samples := len(quality.PlanSamples(duration))
	a.emitEvent("enkoder-log", tr("quality_start", metric, fmtScore(metric, target), samples, opt.CQ))
	search := &quality.Search{
		FFmpeg:   a.ffmpegPath,
		Base:     opt,
		Duration: duration,
		Metric:   metric,
		Target:   target,
		Level:    quality.Level(level),
		Start:    opt.CQ,
	}
	trial := 0
	search.OnSample = func(crf, sample, n int) {
		// The search usually ends after 4–5 values; the bar counts up to
		// MaxTrials and jumps to 100% when done.
		pct := float64(trial*n+sample-1) / float64(quality.MaxTrials*n) * 100
		a.emitJSON("enkoder-quality-progress", map[string]any{
			"percent": pct, "text": tr("quality_step", crf, sample, n),
		})
	}
	search.OnTrial = func(t quality.Trial, n int) {
		trial = n
		mark := "✗"
		if t.Score >= target {
			mark = "✓"
		}
		per := make([]string, len(t.Scores))
		for i, v := range t.Scores {
			per[i] = strings.TrimSuffix(fmtScore(metric, v), " dB")
		}
		a.emitEvent("enkoder-log", tr("quality_trial", t.CRF, metric, fmtScore(metric, t.Score), mark, strings.Join(per, ", ")))
	}

	go func() {
		defer cancel()
		res, err := search.Run(ctx)

		a.mu.Lock()
		a.busy = false
		a.cancel = nil
		a.mu.Unlock()

		switch {
		case ctx.Err() != nil:
			a.emitEvent("enkoder-cancelled", "")
		case err != nil:
			a.emitEvent("enkoder-error", err.Error())
		default:
			if res.Met {
				a.emitEvent("enkoder-log", tr("quality_found", res.CRF, metric, fmtScore(metric, res.Score), formatBytes(res.EstimatedBytes)))
			} else {
				a.emitEvent("enkoder-log", tr("quality_not_met", res.CRF, metric, fmtScore(metric, res.Score)))
			}
			a.emitJSON("enkoder-quality", qualityDTO{
				CRF: res.CRF, Score: res.Score, Target: res.Target, Metric: string(res.Metric),
				Met: res.Met, EstimatedBytes: res.EstimatedBytes, Trials: len(res.Trials),
			})
		}
	}()
	return nil
}

func fmtScore(m quality.Metric, v float64) string {
	switch m {
	case quality.MetricSSIM:
		return strconv.FormatFloat(v, 'f', 4, 64)
	case quality.MetricXPSNR:
		return strconv.FormatFloat(v, 'f', 2, 64) + " dB"
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func formatBytes(n int64) string {
	const mb = 1 << 20
	if n >= 1<<30 {
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', 2, 64) + " GB"
	}
	return strconv.FormatFloat(float64(n)/mb, 'f', 0, 64) + " MB"
}

// emitJSON dispatches a CustomEvent whose detail is v as a JS object.
func (a *app) emitJSON(name string, v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}
	js := fmt.Sprintf(`window.dispatchEvent(new CustomEvent(%q,{detail:%s}))`, name, string(payload))
	a.view.Dispatch(func() {
		a.view.Eval(js)
	})
}

func (a *app) pumpLogs(logCh <-chan string) {
	const flushEvery = 120 * time.Millisecond
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()

	var batch []string
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Join batch into one Eval to reduce UI thrash.
		text := strings.Join(batch, "\n")
		batch = batch[:0]
		a.emitEvent("enkoder-log", text)
	}

	for {
		select {
		case line, ok := <-logCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, line)
			if len(batch) >= 12 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (a *app) emitEvent(name, detail string) {
	payload, err := json.Marshal(detail)
	if err != nil {
		payload = []byte(`""`)
	}
	js := fmt.Sprintf(`window.dispatchEvent(new CustomEvent(%q,{detail:%s}))`, name, string(payload))
	a.view.Dispatch(func() {
		a.view.Eval(js)
	})
}

func (a *app) emitProgress(percent, currentSec, durationSec float64) {
	payload, err := json.Marshal(map[string]float64{
		"percent":  percent,
		"current":  currentSec,
		"duration": durationSec,
	})
	if err != nil {
		return
	}
	// Call applyEncodeProgress directly (same Eval pattern as applyCapabilities /
	// applySystemStats on Linux). Keep CustomEvent as a fallback.
	js := fmt.Sprintf(`(function(){
  var d = %s;
  if (typeof window.applyEncodeProgress === "function") { window.applyEncodeProgress(d); }
  else { window.dispatchEvent(new CustomEvent("enkoder-progress",{detail:d})); }
})()`, string(payload))
	a.view.Dispatch(func() {
		a.view.Eval(js)
	})
}

func (a *app) probeDuration(input string) float64 {
	input = strings.TrimSpace(input)
	if input == "" || a.ffprobePath == "" {
		return 0
	}
	res, err := ffprobe.Probe(a.ffprobePath, input)
	if err != nil {
		return 0
	}
	return ffprobe.DurationSeconds(res)
}

func (a *app) cancelEncode() {
	a.mu.Lock()
	cancel := a.cancel
	cmd := a.cmd
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (a *app) jobToOptions(j jobDTO) (ffmpeg.Options, error) {
	if strings.TrimSpace(j.Input) == "" {
		return ffmpeg.Options{}, errors.New(tr("no_input"))
	}
	if strings.TrimSpace(j.Output) == "" {
		return ffmpeg.Options{}, errors.New(tr("no_output"))
	}
	if sameFile(j.Input, j.Output) {
		return ffmpeg.Options{}, errors.New(tr("same_file"))
	}
	if len(j.Video) == 0 {
		return ffmpeg.Options{}, errors.New(tr("no_video"))
	}
	codec := ffmpeg.VideoCodec(j.Codec)
	switch codec {
	case ffmpeg.CodecHEVCNVENC:
		if !a.nvenc {
			return ffmpeg.Options{}, errors.New(tr("nvenc_missing"))
		}
	case ffmpeg.CodecSVTAV1:
		if !ffmpeg.HasSVTAV1(a.ffmpegPath) {
			return ffmpeg.Options{}, errors.New(tr("svtav1_missing"))
		}
	case ffmpeg.CodecLibX265, ffmpeg.CodecCopy:
	default:
		codec = ffmpeg.VideoCodec(a.defaultCodec)
	}
	if j.ScaleHeight > 0 && codec == ffmpeg.CodecCopy {
		return ffmpeg.Options{}, errors.New(tr("scale_needs_enc"))
	}
	if j.Deinterlace && codec == ffmpeg.CodecCopy {
		return ffmpeg.Options{}, errors.New(tr("deint_needs_enc"))
	}
	switch j.ScaleHeight {
	case 0, 2160, 1080, 720, 480:
	default:
		return ffmpeg.Options{}, errors.New(tr("bad_resolution", j.ScaleHeight))
	}

	cq := 18
	maxCQ := 51
	if codec == ffmpeg.CodecSVTAV1 {
		cq, maxCQ = 18+ffmpeg.AV1CRFOffset, 63
	}
	if j.CQ != nil {
		if *j.CQ < 0 || *j.CQ > maxCQ {
			return ffmpeg.Options{}, errors.New(tr("bad_cq", maxCQ))
		}
		cq = *j.CQ
	}

	masterDisplay := strings.TrimSpace(j.MasterDisplay)
	if masterDisplay != "" && !reMasterDisplay.MatchString(masterDisplay) {
		return ffmpeg.Options{}, errors.New(tr("bad_master", masterDisplay))
	}
	maxCLL := strings.TrimSpace(j.MaxCLL)
	if maxCLL != "" && !reMaxCLL.MatchString(maxCLL) {
		return ffmpeg.Options{}, errors.New(tr("bad_maxcll", maxCLL))
	}

	preset := strings.TrimSpace(j.Preset)
	if err := validatePreset(codec, preset); err != nil {
		return ffmpeg.Options{}, err
	}
	if preset == "" && codec != ffmpeg.CodecCopy {
		preset = ffmpeg.DefaultPreset(codec)
	}

	tune := strings.TrimSpace(j.Tune)
	switch tune {
	case "", "film", "animation", "grain":
	default:
		return ffmpeg.Options{}, errors.New(tr("bad_tune", tune))
	}
	if tune != "" && codec != ffmpeg.CodecLibX265 {
		return ffmpeg.Options{}, errors.New(tr("tune_x265_only"))
	}

	speed := ffmpeg.X265Speed(strings.TrimSpace(j.Speed))
	switch speed {
	case "":
		speed = ffmpeg.SpeedBalanced // the UI default
	case ffmpeg.SpeedQuality, ffmpeg.SpeedBalanced, ffmpeg.SpeedFast:
	default:
		return ffmpeg.Options{}, errors.New(tr("bad_speed", speed))
	}

	pixFmt := strings.TrimSpace(j.PixFmt)
	switch pixFmt {
	case "", "yuv420p", "yuv420p10le", "auto":
		if pixFmt == "auto" {
			pixFmt = ""
		}
	default:
		return ffmpeg.Options{}, errors.New(tr("bad_pixfmt", pixFmt))
	}

	reencode := codec != ffmpeg.CodecCopy
	var opt ffmpeg.Options
	x265 := codec == ffmpeg.CodecLibX265
	if !x265 {
		speed = ""
	}
	cpuEncoder := x265 || codec == ffmpeg.CodecSVTAV1
	opt = ffmpeg.Options{
		Input:          j.Input,
		Output:         j.Output,
		VideoMaps:      j.Video,
		AudioMaps:      j.Audio,
		SubtitleMaps:   j.Subtitle,
		VideoCodec:     codec,
		Container:      j.Container,
		CQ:             cq,
		Preset:         preset,
		Tune:           tune,
		PixFmt:         pixFmt,
		ScaleHeight:    j.ScaleHeight,
		Deinterlace:    j.Deinterlace && reencode,
		HDR:            j.HDR && reencode,
		ColorTransfer:  j.ColorTransfer,
		ColorPrimaries: j.ColorPrimaries,
		ColorSpace:     j.ColorSpace,
		CopyAudio:      true,
		CopySubs:       true,
		MapMetadata:    reencode,
		MapChapters:    reencode,
		Speed:          speed,
		AVX512:         x265 && hasAVX512(),
		HWDecode:       cpuEncoder && a.nvenc && ffmpeg.HasCUDA(a.ffmpegPath),
		NVENCTuned:     codec == ffmpeg.CodecHEVCNVENC && ffmpeg.HasNVENCTuned(a.ffmpegPath),
		MasterDisplay:  masterDisplay,
		MaxCLL:         maxCLL,
	}
	if codec == ffmpeg.CodecHEVCNVENC && (opt.ScaleHeight > 0 || opt.Deinterlace) {
		opt.GPUFilters = a.canGPUFilter(opt)
	}
	return opt, nil
}

var (
	// Strict formats: these strings end up inside -x265-params, where a stray
	// ':' would inject arbitrary encoder options.
	reMasterDisplay = regexp.MustCompile(`^G\(\d+,\d+\)B\(\d+,\d+\)R\(\d+,\d+\)WP\(\d+,\d+\)L\(\d+,\d+\)$`)
	reMaxCLL        = regexp.MustCompile(`^\d+,\d+$`)
)

// canGPUFilter caches ffmpeg.CanGPUFilter per input and filter chain, so
// "Show command" and "Encode" decode the test frame only once.
func (a *app) canGPUFilter(opt ffmpeg.Options) bool {
	if !a.nvenc || !ffmpeg.HasCUDA(a.ffmpegPath) {
		return false
	}
	key := fmt.Sprintf("%s|%v|%d|%v|%s", opt.Input, opt.VideoMaps, opt.ScaleHeight, opt.Deinterlace, opt.PixFmt)
	a.gpuFilterMu.Lock()
	defer a.gpuFilterMu.Unlock()
	if ok, cached := a.gpuFilterCache[key]; cached {
		return ok
	}
	ok := ffmpeg.CanGPUFilter(a.ffmpegPath, opt)
	if a.gpuFilterCache == nil {
		a.gpuFilterCache = map[string]bool{}
	}
	a.gpuFilterCache[key] = ok
	return ok
}

func validatePreset(codec ffmpeg.VideoCodec, preset string) error {
	if preset == "" || codec == ffmpeg.CodecCopy {
		return nil
	}
	switch codec {
	case ffmpeg.CodecLibX265:
		switch preset {
		case "ultrafast", "superfast", "veryfast", "faster", "fast",
			"medium", "slow", "slower", "veryslow", "placebo":
			return nil
		}
	case ffmpeg.CodecHEVCNVENC:
		switch preset {
		case "p1", "p2", "p3", "p4", "p5", "p6", "p7":
			return nil
		}
	case ffmpeg.CodecSVTAV1:
		if n, err := strconv.Atoi(preset); err == nil && n >= 0 && n <= 13 {
			return nil
		}
	}
	return errors.New(tr("bad_preset", preset, codec))
}

// sameFile reports whether both paths name the same existing file, also
// through symlinks, hard links or a different letter case on Windows.
func sameFile(a, b string) bool {
	ai, err := os.Stat(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	bi, err := os.Stat(strings.TrimSpace(b))
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// removePartialOutput deletes the output of a failed or cancelled encode.
// before is the file's state ahead of the run (nil if it did not exist): a
// file ffmpeg never opened (same size and mtime) is left alone, so a run
// that failed early does not delete an older file the user still has.
func removePartialOutput(path string, before os.FileInfo) (bool, error) {
	now, err := os.Lstat(path)
	if err != nil {
		return false, nil // nothing was written
	}
	if !now.Mode().IsRegular() {
		return false, nil
	}
	if before != nil && os.SameFile(before, now) &&
		before.Size() == now.Size() && before.ModTime().Equal(now.ModTime()) {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

// fileExists is true for anything already at path (file, directory, link).
func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// confirmOverwrite asks before ffmpeg replaces an existing output file.
var confirmOverwrite = func(path string) bool {
	err := zenity.Question(tr("overwrite_q", path),
		zenity.Title(appTitle()),
		zenity.WarningIcon,
		zenity.OKLabel(tr("overwrite_ok")),
		zenity.CancelLabel(tr("overwrite_cancel")))
	return err == nil
}

func suggestExt(container string) string {
	switch strings.ToLower(container) {
	case "mp4":
		return "mp4"
	default:
		return "mkv"
	}
}

func encodeDataURL(html string) string {
	replacer := strings.NewReplacer(
		"%", "%25",
		"#", "%23",
		"?", "%3F",
		" ", "%20",
		"\n", "%0A",
		"\r", "%0D",
		`"`, "%22",
		"<", "%3C",
		">", "%3E",
	)
	return replacer.Replace(html)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

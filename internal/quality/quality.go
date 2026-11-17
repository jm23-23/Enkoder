// Package quality finds the highest CRF / CQ that still meets a quality
// target: it encodes a few short samples of the source at candidate values,
// measures them against the source with a full-reference metric (VMAF,
// XPSNR or SSIM, whichever this ffmpeg has) and searches for the boundary.
package quality

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"enkoder/internal/ffmpeg"
	"enkoder/internal/procattr"
)

// Metric is a full-reference video quality metric ffmpeg can compute.
type Metric string

const (
	MetricVMAF  Metric = "vmaf"  // libvmaf, 0–100
	MetricXPSNR Metric = "xpsnr" // dB, ffmpeg ≥ 7.1
	MetricSSIM  Metric = "ssim"  // 0–1
)

// Level is the quality target the user picks.
type Level string

const (
	LevelTransparent Level = "transparent" // visually lossless
	LevelHigh        Level = "high"
	LevelGood        Level = "good"
)

// targets per metric and level. VMAF follows common practice (95 is the
// usual "high" target, 97 near-transparent). XPSNR and SSIM were measured
// on 10 s samples of four 1080p films (two grainy Blu-rays, two WEB-DLs),
// x265 slow / balanced: on the grainy Blu-rays the levels land near CRF 15,
// 18 (Enkoder's default) and 21.
//
//	XPSNR (6:1:1)   CRF 14   18     22     26
//	Blu-ray A       41.79  39.55  37.66  36.10
//	Blu-ray B       42.29  39.16  36.04  33.87
//	WEB-DL C        43.90  42.21  40.78  39.43
//	WEB-DL D        43.90  42.53  41.41  40.28
//
// PSNR-type metrics rate clean, soft sources far higher than grainy ones,
// so on WEB-DLs they would allow CRF 26–30; maxCRF below caps that.
var targets = map[Metric]map[Level]float64{
	MetricVMAF:  {LevelTransparent: 97, LevelHigh: 95, LevelGood: 93},
	MetricXPSNR: {LevelTransparent: 41.5, LevelHigh: 39.5, LevelGood: 37.5},
	MetricSSIM:  {LevelTransparent: 0.985, LevelHigh: 0.975, LevelGood: 0.965},
}

// maxCRF is the highest value a level may choose, whatever the metric says.
// SVT-AV1's scale is wider: on the same Blu-ray sample its CRF 22 matched
// x265 CRF 22 and its CRF ~34 matched x265 CRF 26.
var maxCRF = map[Level][2]int{ // {x265 / NVENC, SVT-AV1}
	LevelTransparent: {20, 20},
	LevelHigh:        {24, 28},
	LevelGood:        {28, 36},
}

// Target returns the score a level means for metric, or false if unknown.
func Target(m Metric, l Level) (float64, bool) {
	t, ok := targets[m][l]
	return t, ok
}

// DetectMetric returns the best metric this ffmpeg can compute, or "".
func DetectMetric(ffmpegPath string) Metric {
	cmd := exec.Command(ffmpegPath, "-hide_banner", "-filters")
	procattr.HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return metricFromFilters(string(out))
}

func metricFromFilters(list string) Metric {
	has := map[string]bool{}
	for _, line := range strings.Split(list, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			has[f[1]] = true
		}
	}
	for _, m := range []struct {
		filter string
		metric Metric
	}{{"libvmaf", MetricVMAF}, {"xpsnr", MetricXPSNR}, {"ssim", MetricSSIM}} {
		if has[m.filter] {
			return m.metric
		}
	}
	return ""
}

// Sample is a piece of the source, in seconds.
type Sample struct {
	Start, Length float64
}

// PlanSamples spreads samples over the film, skipping the very start and
// end (logos, credits). Short sources are measured whole.
func PlanSamples(duration float64) []Sample {
	const length = 10.0
	if duration <= 0 {
		return nil
	}
	if duration <= 4*length {
		return []Sample{{0, duration}}
	}
	n := int(duration/1200) + 3 // 3 samples up to 20 min, +1 per 20 min
	if n > 6 {
		n = 6
	}
	samples := make([]Sample, n)
	for i := range samples {
		start := duration*float64(i+1)/float64(n+1) - length/2
		samples[i] = Sample{math.Round(start*1000) / 1000, length}
	}
	return samples
}

// Trial is one candidate value measured on all samples.
type Trial struct {
	CRF int
	// Score is the worst sample's score: every sampled scene has to meet
	// the target, not just the average (easy scenes would hide hard ones).
	Score float64
	// Scores are the per-sample scores, in sample order.
	Scores []float64
	// Bytes is the total size of the encoded samples.
	Bytes int64
}

// Result is the outcome of a search.
type Result struct {
	Metric Metric
	Target float64
	// CRF is the highest value that met the target (at most the level's
	// cap); if none did, the lowest value tried (Met is false then).
	CRF   int
	Score float64
	Met   bool
	// EstimatedBytes is the expected size of the video stream at CRF,
	// extrapolated from the samples.
	EstimatedBytes int64
	Trials         []Trial
}

// Search describes one search; Base is the encode as the user set it up.
type Search struct {
	FFmpeg   string
	Base     ffmpeg.Options
	Duration float64
	Metric   Metric
	Target   float64
	// Level caps the CRF (see Range); "" searches the whole range.
	Level Level
	// Start is the first value tried, normally the current CQ/CRF.
	Start int
	// TempDir holds the sample encodes; "" uses the system default.
	TempDir string
	// OnTrial is called after each measured value.
	OnTrial func(t Trial, n int)
	// OnSample is called before each sample encode (for progress).
	OnSample func(crf, sample, samples int)
}

// MaxTrials bounds the number of values measured.
const MaxTrials = 7

// Range returns the CRF / CQ range searched for codec and level.
func Range(codec ffmpeg.VideoCodec, l Level) (lo, hi int) {
	av1 := codec == ffmpeg.CodecSVTAV1
	lo, hi = 8, 34
	if av1 {
		lo, hi = 10, 55
	}
	if c, ok := maxCRF[l]; ok {
		hi = c[0]
		if av1 {
			hi = c[1]
		}
	}
	return lo, hi
}

// Run performs the search. It stops early when ctx is cancelled.
func (s *Search) Run(ctx context.Context) (Result, error) {
	if s.Base.VideoCodec == ffmpeg.CodecCopy || s.Base.VideoCodec == "" {
		return Result{}, errors.New("a target quality needs a re-encode")
	}
	if len(s.Base.VideoMaps) == 0 {
		return Result{}, errors.New("no video track")
	}
	samples := PlanSamples(s.Duration)
	if len(samples) == 0 {
		return Result{}, errors.New("unknown source duration")
	}
	dir, err := os.MkdirTemp(s.TempDir, "enkoder-quality-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	lo, hi := Range(s.Base.VideoCodec, s.Level)
	measure := func(crf int) (Trial, error) {
		return s.measure(ctx, dir, samples, crf)
	}
	trials, err := search(s.Start, lo, hi, s.Target, measure, func(t Trial, n int) {
		if s.OnTrial != nil {
			s.OnTrial(t, n)
		}
	})
	if err != nil {
		return Result{}, err
	}
	res := pick(trials, s.Target)
	res.Metric, res.Target = s.Metric, s.Target
	var sampled float64
	for _, smp := range samples {
		sampled += smp.Length
	}
	for _, t := range trials {
		if t.CRF == res.CRF && sampled > 0 {
			res.EstimatedBytes = int64(float64(t.Bytes) / sampled * s.Duration)
		}
	}
	return res, nil
}

// search finds the boundary between passing (score ≥ target) and failing
// values, assuming the score falls as CRF rises. It steps by 4 from start
// until both sides are known, then bisects.
func search(start, lo, hi int, target float64, measure func(int) (Trial, error), onTrial func(Trial, int)) ([]Trial, error) {
	if start < lo || start > hi {
		start = (lo + hi) / 2
	}
	var trials []Trial
	pass, fail := lo-1, hi+1 // highest known pass, lowest known fail
	next := start
	for len(trials) < MaxTrials {
		t, err := measure(next)
		if err != nil {
			return trials, err
		}
		trials = append(trials, t)
		onTrial(t, len(trials))
		if t.Score >= target {
			pass = max(pass, t.CRF)
		} else {
			fail = min(fail, t.CRF)
		}
		switch {
		case fail-pass <= 1:
			return trials, nil // boundary found
		case pass < lo: // only failures so far: go down
			next = max(lo, fail-4)
		case fail > hi: // only passes so far: go up
			next = min(hi, pass+4)
		default:
			next = (pass + fail) / 2
		}
		if measured(trials, next) {
			return trials, nil // range exhausted (lo or hi reached)
		}
	}
	return trials, nil
}

func measured(trials []Trial, crf int) bool {
	for _, t := range trials {
		if t.CRF == crf {
			return true
		}
	}
	return false
}

// pick chooses the highest passing value, or the lowest tried if none passed.
func pick(trials []Trial, target float64) Result {
	var best *Trial
	for i := range trials {
		t := &trials[i]
		if t.Score >= target && (best == nil || t.CRF > best.CRF) {
			best = t
		}
	}
	if best != nil {
		return Result{CRF: best.CRF, Score: best.Score, Met: true, Trials: trials}
	}
	for i := range trials {
		t := &trials[i]
		if best == nil || t.CRF < best.CRF {
			best = t
		}
	}
	if best == nil {
		return Result{}
	}
	return Result{CRF: best.CRF, Score: best.Score, Trials: trials}
}

// measure encodes every sample at crf and scores it by the worst sample.
func (s *Search) measure(ctx context.Context, dir string, samples []Sample, crf int) (Trial, error) {
	t := Trial{CRF: crf}
	for i, smp := range samples {
		if s.OnSample != nil {
			s.OnSample(crf, i+1, len(samples))
		}
		out := filepath.Join(dir, fmt.Sprintf("crf%d_%d.mkv", crf, i))
		if err := s.run(ctx, encodeArgs(s.Base, smp, crf, out)); err != nil {
			return t, fmt.Errorf("sample encode (CRF %d): %w", crf, err)
		}
		if st, err := os.Stat(out); err == nil {
			t.Bytes += st.Size()
		}
		stderr, err := s.output(ctx, metricArgs(s.Base, s.Metric, smp, out))
		if err != nil {
			return t, fmt.Errorf("quality measurement (CRF %d): %w", crf, err)
		}
		score, ok := ParseScore(s.Metric, stderr)
		if !ok {
			return t, fmt.Errorf("could not read the %s score", s.Metric)
		}
		t.Scores = append(t.Scores, score)
		if i == 0 || score < t.Score {
			t.Score = score
		}
		os.Remove(out)
	}
	return t, nil
}

// encodeArgs is the user's encode limited to one sample, video only.
func encodeArgs(base ffmpeg.Options, smp Sample, crf int, out string) []string {
	opt := base
	opt.Output = out
	opt.CQ = crf
	opt.VideoMaps = base.VideoMaps[:1]
	opt.AudioMaps, opt.SubtitleMaps = nil, nil
	opt.CopyAudio, opt.CopySubs = false, false
	opt.MapMetadata, opt.MapChapters = false, false
	args := ffmpeg.BuildArgs(opt)
	return withSeek(args, smp)
}

// withSeek puts -ss/-t in front of the input and quiets the log.
func withSeek(args []string, smp Sample) []string {
	out := make([]string, 0, len(args)+8)
	out = append(out, "-v", "error", "-nostdin")
	for _, a := range args {
		if a == "-stats" {
			continue
		}
		if a == "-i" {
			out = append(out, "-ss", fmtSec(smp.Start), "-t", fmtSec(smp.Length))
		}
		out = append(out, a)
	}
	return out
}

// metricArgs compares an encoded sample with the same piece of the source,
// passed through the encode's deinterlace / scale filters. Both sides are
// converted to 10-bit 4:2:0 so the metric sees the same format.
//
// Frames are paired by index, not by timestamp: after an input seek the
// encode and the reference round their timestamps differently (e.g. first
// frame at 0.042 vs 0.035 s), and the metric's frame sync then compares
// neighbouring frames now and then — a 1080p WEB-DL sample scored 35 dB
// instead of 43 at CRF 8 that way.
func metricArgs(base ffmpeg.Options, m Metric, smp Sample, encoded string) []string {
	const norm = "settb=1/25,setpts=N,format=yuv420p10le"
	ref := norm
	if f := ffmpeg.ReferenceFilter(base); f != "" {
		ref = f + "," + norm
	}
	var cmp string
	switch m {
	case MetricVMAF:
		cmp = "libvmaf=n_threads=" + strconv.Itoa(runtime.NumCPU())
	case MetricXPSNR:
		cmp = "xpsnr"
	default:
		cmp = "ssim"
	}
	graph := fmt.Sprintf("[0:v]%s[d];[1:%d]%s[r];[d][r]%s", norm, base.VideoMaps[0], ref, cmp)
	return []string{"-hide_banner", "-nostdin", "-nostats",
		"-i", encoded,
		"-ss", fmtSec(smp.Start), "-t", fmtSec(smp.Length), "-i", base.Input,
		"-lavfi", graph, "-f", "null", "-"}
}

var (
	reVMAF  = regexp.MustCompile(`VMAF score:\s*([0-9.]+)`)
	reXPSNR = regexp.MustCompile(`XPSNR\s+y:\s*([0-9.]+|inf)\s+u:\s*([0-9.]+|inf)\s+v:\s*([0-9.]+|inf)`)
	reSSIM  = regexp.MustCompile(`SSIM .*All:([0-9.]+)`)
)

// ParseScore reads the final score from ffmpeg's log. XPSNR is the usual
// 6:1:1 weighting of the luma and chroma planes; identical planes (inf)
// count as 100 dB.
func ParseScore(m Metric, log string) (float64, bool) {
	switch m {
	case MetricVMAF:
		if v := lastMatch(reVMAF, log); v != nil {
			return parseF(v[1])
		}
	case MetricXPSNR:
		if v := lastMatch(reXPSNR, log); v != nil {
			y, ok1 := parseF(v[1])
			u, ok2 := parseF(v[2])
			w, ok3 := parseF(v[3])
			if ok1 && ok2 && ok3 {
				return (6*y + u + w) / 8, true
			}
		}
	case MetricSSIM:
		if v := lastMatch(reSSIM, log); v != nil {
			return parseF(v[1])
		}
	}
	return 0, false
}

func lastMatch(re *regexp.Regexp, s string) []string {
	all := re.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

func parseF(s string) (float64, bool) {
	if s == "inf" {
		return 100, true
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && !math.IsNaN(v)
}

func fmtSec(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

func (s *Search) run(ctx context.Context, args []string) error {
	_, err := s.output(ctx, args)
	return err
}

// output runs ffmpeg and returns its stderr (where the metrics are logged).
func (s *Search) output(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, s.FFmpeg, args...)
	procattr.HideConsole(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, lastLines(stderr.String(), 3))
	}
	return stderr.String(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

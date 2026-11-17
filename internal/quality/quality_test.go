package quality

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"enkoder/internal/ffmpeg"
)

func TestPlanSamples(t *testing.T) {
	if s := PlanSamples(0); s != nil {
		t.Fatalf("no duration: %v", s)
	}
	if s := PlanSamples(30); len(s) != 1 || s[0] != (Sample{0, 30}) {
		t.Fatalf("short source: %v", s)
	}
	for dur, n := range map[float64]int{600: 3, 1200: 4, 2 * 3600: 6, 5 * 3600: 6} {
		s := PlanSamples(dur)
		if len(s) != n {
			t.Errorf("%v s: %d samples, want %d", dur, len(s), n)
		}
		for i, smp := range s {
			if smp.Start <= 0 || smp.Start+smp.Length >= dur {
				t.Errorf("%v s: sample %d out of range: %+v", dur, i, smp)
			}
			if i > 0 && smp.Start <= s[i-1].Start {
				t.Errorf("%v s: samples not in order", dur)
			}
		}
	}
}

// curve is a fake encoder: the score falls linearly with CRF.
func curve(top, slope float64) func(int) (Trial, error) {
	return func(crf int) (Trial, error) {
		return Trial{CRF: crf, Score: top - slope*float64(crf), Bytes: int64(1000 / (crf + 1))}, nil
	}
}

func TestSearchFindsBoundary(t *testing.T) {
	// score = 50 - 0.5*crf; target 40 → highest passing CRF is 20.
	for _, start := range []int{8, 12, 18, 20, 21, 26, 34} {
		trials, err := search(start, 8, 34, 40, curve(50, 0.5), func(Trial, int) {})
		if err != nil {
			t.Fatal(err)
		}
		res := pick(trials, 40)
		if !res.Met || res.CRF != 20 {
			t.Errorf("start %d: CRF %d met=%v after %d trials", start, res.CRF, res.Met, len(trials))
		}
		if len(trials) > MaxTrials {
			t.Errorf("start %d: %d trials", start, len(trials))
		}
	}
}

func TestSearchAtTheEdges(t *testing.T) {
	// Even the best value fails: report the lowest tried, not met.
	trials, _ := search(18, 8, 34, 99, curve(50, 0.5), func(Trial, int) {})
	if res := pick(trials, 99); res.Met || res.CRF != 8 {
		t.Errorf("unreachable: %+v", res)
	}
	// Everything passes: the top of the range.
	trials, _ = search(18, 8, 34, 1, curve(50, 0.5), func(Trial, int) {})
	if res := pick(trials, 1); !res.Met || res.CRF != 34 {
		t.Errorf("always met: %+v", res)
	}
}

func TestSearchStopsOnError(t *testing.T) {
	boom := errors.New("boom")
	n := 0
	_, err := search(18, 8, 34, 40, func(crf int) (Trial, error) {
		n++
		if n == 2 {
			return Trial{}, boom
		}
		return curve(50, 0.5)(crf)
	}, func(Trial, int) {})
	if !errors.Is(err, boom) || n != 2 {
		t.Fatalf("err=%v after %d", err, n)
	}
}

func TestParseScore(t *testing.T) {
	cases := []struct {
		m    Metric
		log  string
		want float64
	}{
		{MetricVMAF, "[Parsed_libvmaf_4 @ 0x1] VMAF score: 95.123456\n", 95.123456},
		{MetricXPSNR, "[Parsed_xpsnr_4 @ 0x1] XPSNR  y: 40.0000  u: 48.0000  v: 32.0000  (minimum: 32.0000)\n", 40},
		{MetricXPSNR, "XPSNR  y: inf  u: inf  v: inf  (minimum: inf)", 100},
		{MetricSSIM, "[Parsed_ssim_4 @ 0x1] SSIM Y:0.986733 (18.77) U:0.98 (18.0) V:0.99 (21.6) All:0.987403 (18.99)", 0.987403},
	}
	for _, c := range cases {
		got, ok := ParseScore(c.m, c.log)
		if !ok || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got %v ok=%v, want %v", c.m, got, ok, c.want)
		}
	}
	if _, ok := ParseScore(MetricXPSNR, "nothing here"); ok {
		t.Error("parsed a score from an empty log")
	}
}

func TestMetricFromFilters(t *testing.T) {
	base := " TS ssim              VV->V      Calculate the SSIM\n"
	if m := metricFromFilters(base); m != MetricSSIM {
		t.Errorf("ssim only: %q", m)
	}
	x := base + " T. xpsnr             VV->V      XPSNR\n"
	if m := metricFromFilters(x); m != MetricXPSNR {
		t.Errorf("xpsnr: %q", m)
	}
	// vmafmotion is not libvmaf.
	if m := metricFromFilters(x + " .. vmafmotion        V->V       VMAF Motion\n"); m != MetricXPSNR {
		t.Errorf("vmafmotion: %q", m)
	}
	if m := metricFromFilters(x + " .. libvmaf           VV->V      VMAF\n"); m != MetricVMAF {
		t.Errorf("libvmaf: %q", m)
	}
	if m := metricFromFilters(""); m != "" {
		t.Errorf("none: %q", m)
	}
}

func TestEncodeArgsSampleOnly(t *testing.T) {
	base := ffmpeg.Options{
		Input: "in.mkv", Output: "final.mkv", VideoMaps: []int{0}, AudioMaps: []int{1}, SubtitleMaps: []int{2},
		VideoCodec: ffmpeg.CodecLibX265, CQ: 18, Preset: "slow", CopyAudio: true, CopySubs: true,
		MapMetadata: true, MapChapters: true, ScaleHeight: 720,
	}
	args := strings.Join(encodeArgs(base, Sample{100, 10}, 23, "s.mkv"), " ")
	for _, want := range []string{"-ss 100.000 -t 10.000 -i in.mkv", "-crf 23", "-map 0:0", "scale=", "s.mkv"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	for _, bad := range []string{"0:1", "0:2", "-c:a", "-map_metadata", "final.mkv", "-stats"} {
		if strings.Contains(args, bad) {
			t.Errorf("unexpected %q in %s", bad, args)
		}
	}
}

func TestMetricArgsMatchGeometry(t *testing.T) {
	base := ffmpeg.Options{Input: "in.mkv", VideoMaps: []int{3}, VideoCodec: ffmpeg.CodecHEVCNVENC,
		GPUFilters: true, ScaleHeight: 1080, Deinterlace: true}
	args := strings.Join(metricArgs(base, MetricXPSNR, Sample{50, 10}, "s.mkv"), " ")
	// The reference goes through the CPU equivalents of the GPU filters.
	for _, want := range []string{"-i s.mkv", "-ss 50.000 -t 10.000 -i in.mkv", "[1:3]bwdif,scale=", "[d][r]xpsnr"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if strings.Contains(args, "cuda") {
		t.Errorf("reference uses CUDA filters: %s", args)
	}
}

// TestSearchRealFFmpeg runs a whole search on a generated clip.
func TestSearchRealFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	m := DetectMetric(ff)
	if m == "" {
		t.Skip("no metric")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25",
		"-t", "60", "-c:v", "ffv1", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	high, _ := Target(m, LevelHigh)
	var trials []Trial
	s := &Search{
		FFmpeg: ff, Duration: 60, Metric: m, Target: high, Start: 18, TempDir: dir,
		Base: ffmpeg.Options{Input: src, VideoMaps: []int{0}, VideoCodec: ffmpeg.CodecLibX265,
			Preset: "ultrafast", PixFmt: "yuv420p"},
		OnTrial: func(tr Trial, n int) { trials = append(trials, tr) },
	}
	res, err := s.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s target %.3f → CRF %d score %.3f met=%v est=%d B, %d trials", m, high, res.CRF, res.Score, res.Met, res.EstimatedBytes, len(trials))
	if len(trials) == 0 || len(trials) > MaxTrials {
		t.Fatalf("%d trials", len(trials))
	}
	if res.Met && res.Score < high {
		t.Fatalf("met with score %v < %v", res.Score, high)
	}
	if res.EstimatedBytes <= 0 {
		t.Fatal("no size estimate")
	}
	// Scores must fall as CRF rises.
	for _, a := range trials {
		for _, b := range trials {
			if a.CRF < b.CRF && a.Score+1e-6 < b.Score {
				t.Errorf("CRF %d scored %v below CRF %d %v", a.CRF, a.Score, b.CRF, b.Score)
			}
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestSearchCancel(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil || DetectMetric(ff) == "" {
		t.Skip("no ffmpeg / metric")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25",
		"-t", "120", "-c:v", "ffv1", src).CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Search{
		FFmpeg: ff, Duration: 120, Metric: DetectMetric(ff), Target: 1000, Start: 18, TempDir: dir,
		Base:     ffmpeg.Options{Input: src, VideoMaps: []int{0}, VideoCodec: ffmpeg.CodecLibX265, Preset: "slow"},
		OnSample: func(int, int, int) { cancel() },
	}
	start := time.Now()
	if _, err := s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("cancel took %v", time.Since(start))
	}
}

func TestRangeCaps(t *testing.T) {
	for _, c := range []struct {
		codec  ffmpeg.VideoCodec
		level  Level
		lo, hi int
	}{
		{ffmpeg.CodecLibX265, LevelHigh, 8, 24},
		{ffmpeg.CodecHEVCNVENC, LevelTransparent, 8, 20},
		{ffmpeg.CodecSVTAV1, LevelGood, 10, 36},
		{ffmpeg.CodecLibX265, "", 8, 34},
	} {
		if lo, hi := Range(c.codec, c.level); lo != c.lo || hi != c.hi {
			t.Errorf("%s/%s: %d–%d, want %d–%d", c.codec, c.level, lo, hi, c.lo, c.hi)
		}
	}
	// Every level has a target for every metric.
	for _, m := range []Metric{MetricVMAF, MetricXPSNR, MetricSSIM} {
		for _, l := range []Level{LevelTransparent, LevelHigh, LevelGood} {
			if _, ok := Target(m, l); !ok {
				t.Errorf("no target for %s/%s", m, l)
			}
		}
	}
}

func TestMetricPairsFramesByIndex(t *testing.T) {
	base := ffmpeg.Options{Input: "in.mkv", VideoMaps: []int{0}, VideoCodec: ffmpeg.CodecLibX265}
	args := strings.Join(metricArgs(base, MetricSSIM, Sample{1, 10}, "s.mkv"), " ")
	if strings.Count(args, "setpts=N") != 2 || strings.Contains(args, "STARTPTS") {
		t.Errorf("frames not paired by index: %s", args)
	}
}

package ffmpeg

import (
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestBuildArgsNVENCUsesCUDA(t *testing.T) {
	args := BuildArgs(Options{
		Input:       "in.mkv",
		Output:      "out.mkv",
		VideoMaps:   []int{0},
		VideoCodec:  CodecHEVCNVENC,
		CQ:          18,
		MapMetadata: true,
		MapChapters: true,
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hwaccel cuda") {
		t.Fatalf("expected cuda hwaccel for nvenc, got: %s", joined)
	}
	if !strings.Contains(joined, "hevc_nvenc") {
		t.Fatalf("expected hevc_nvenc, got: %s", joined)
	}
	if !strings.Contains(joined, "-preset p7") {
		t.Fatalf("expected default NVENC preset p7, got: %s", joined)
	}
	if !strings.Contains(joined, "-map_metadata 0") || !strings.Contains(joined, "-map_chapters 0") {
		t.Fatalf("expected metadata/chapters map when requested, got: %s", joined)
	}
}

func TestBuildArgsLibX265DefaultsMatchAdvisor(t *testing.T) {
	args := BuildArgs(Options{
		Input:       "in.mkv",
		Output:      "out.mkv",
		VideoMaps:   []int{0},
		VideoCodec:  CodecLibX265,
		CQ:          18,
		MapMetadata: true,
		MapChapters: true,
	})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "cuda") {
		t.Fatalf("libx265 must not use cuda, got: %s", joined)
	}
	if !strings.Contains(joined, "libx265") {
		t.Fatalf("expected libx265, got: %s", joined)
	}
	if !strings.Contains(joined, "-preset slow") {
		t.Fatalf("expected encode-advisor default preset slow, got: %s", joined)
	}
	if !strings.Contains(joined, "-pix_fmt yuv420p") {
		t.Fatalf("expected 8-bit pix_fmt, got: %s", joined)
	}
	if !strings.Contains(joined, "-map_metadata 0") {
		t.Fatalf("expected map_metadata, got: %s", joined)
	}
}

func TestBuildArgsCQZeroPreserved(t *testing.T) {
	args := BuildArgs(Options{
		Input:      "in.mkv",
		Output:     "out.mkv",
		VideoMaps:  []int{0},
		VideoCodec: CodecLibX265,
		CQ:         0,
		Preset:     "slow",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-crf 0") {
		t.Fatalf("CQ/CRF 0 must be preserved, got: %s", joined)
	}
	if strings.Contains(joined, "-crf 18") {
		t.Fatalf("CQ/CRF 0 must not become 18, got: %s", joined)
	}
}

func TestBuildArgsHDR10BitAndTune(t *testing.T) {
	args := BuildArgs(Options{
		Input:         "in.mkv",
		Output:        "out.mkv",
		VideoMaps:     []int{0},
		VideoCodec:    CodecLibX265,
		CQ:            20,
		Preset:        "slow",
		Tune:          "film",
		PixFmt:        "yuv420p10le",
		HDR:           true,
		ColorTransfer: "smpte2084",
		MapMetadata:   true,
		MapChapters:   true,
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-pix_fmt yuv420p10le") {
		t.Fatalf("expected 10-bit, got: %s", joined)
	}
	if !strings.Contains(joined, "tune=film") {
		t.Fatalf("expected tune=film in x265-params, got: %s", joined)
	}
	if !strings.Contains(joined, "transfer=smpte2084") || !strings.Contains(joined, "hdr-opt=1") {
		t.Fatalf("expected HDR x265-params, got: %s", joined)
	}
}

func TestBuildArgsDeinterlaceAndScale(t *testing.T) {
	args := BuildArgs(Options{
		Input:       "in.mkv",
		Output:      "out.mkv",
		VideoMaps:   []int{0},
		VideoCodec:  CodecHEVCNVENC,
		CQ:          18,
		ScaleHeight: 1080,
		Deinterlace: true,
	})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "cuda") {
		t.Fatalf("filtering should skip cuda hwaccel, got: %s", joined)
	}
	if !strings.Contains(joined, "bwdif") {
		t.Fatalf("expected bwdif, got: %s", joined)
	}
	if !strings.Contains(joined, "scale=-2:min(ih\\,1080):flags=lanczos") {
		t.Fatalf("expected downscale filter, got: %s", joined)
	}
}

func TestSuggestedCRFTiers(t *testing.T) {
	if SuggestedCRF(720, 480) != 17 {
		t.Fatalf("SD CRF")
	}
	if SuggestedCRF(1280, 720) != 18 {
		t.Fatalf("HD CRF")
	}
	if SuggestedCRF(1920, 1080) != 18 {
		t.Fatalf("FHD CRF")
	}
	if SuggestedCRF(3840, 2160) != 20 {
		t.Fatalf("UHD CRF")
	}
}

func TestSuggestedPixFmt(t *testing.T) {
	if SuggestedPixFmt(false, 8) != "yuv420p" {
		t.Fatal("8-bit SDR")
	}
	if SuggestedPixFmt(true, 8) != "yuv420p10le" {
		t.Fatal("HDR")
	}
	if SuggestedPixFmt(false, 10) != "yuv420p10le" {
		t.Fatal("10-bit")
	}
}

func TestHasHEVCNVENCLive(t *testing.T) {
	path := ffmpegTestPath()
	if _, err := os.Stat(path); err != nil {
		t.Skip("ffmpeg not available")
	}
	ResetNVENCCache()
	ok := HasHEVCNVENC(path)
	t.Logf("HasHEVCNVENC(%s) = %v", path, ok)
	def := DefaultVideoCodec(path)
	if ok && def != CodecHEVCNVENC {
		t.Fatalf("expected default hevc_nvenc when available, got %s", def)
	}
	if !ok && def != CodecLibX265 {
		t.Fatalf("expected default libx265 when unavailable, got %s", def)
	}
}

func TestBuildArgsScaleDown(t *testing.T) {
	args := BuildArgs(Options{
		Input:       "in.mkv",
		Output:      "out.mkv",
		VideoMaps:   []int{0},
		VideoCodec:  CodecHEVCNVENC,
		CQ:          18,
		ScaleHeight: 1080,
	})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "cuda") {
		t.Fatalf("scaling should skip cuda hwaccel, got: %s", joined)
	}
	if !strings.Contains(joined, "scale=-2:min(ih\\,1080):flags=lanczos") {
		t.Fatalf("expected downscale filter, got: %s", joined)
	}
}

func TestParseStatsTime(t *testing.T) {
	cases := []struct {
		line string
		want float64
		ok   bool
	}{
		{
			line: "frame= 10 fps=30 time=01:02:03.50 bitrate=1000kbits/s",
			want: 1*3600 + 2*60 + 3.5,
			ok:   true,
		},
		{
			line: "frame=   80 fps=0.0 q=36.0 size=       3KiB time=00:00:02.60 bitrate=   9.2kbits/s speed= 5.2x elapsed=0:00:00.50",
			want: 2.60,
			ok:   true,
		},
		{
			line: "frame=1 fps=0.0 q=0.0 size=0kB time=N/A bitrate=N/A",
			ok:   false,
		},
	}
	for _, tc := range cases {
		sec, ok := ParseStatsTime(tc.line)
		if ok != tc.ok {
			t.Fatalf("ok=%v want %v for %q", ok, tc.ok, tc.line)
		}
		if !tc.ok {
			continue
		}
		if sec < tc.want-0.01 || sec > tc.want+0.01 {
			t.Fatalf("got %v want %v for %q", sec, tc.want, tc.line)
		}
	}
}

func TestParseInputDuration(t *testing.T) {
	sec, ok := ParseInputDuration("  Duration: 01:02:03.45, start: 0.000000, bitrate: 1000 kb/s")
	if !ok {
		t.Fatal("expected ok")
	}
	want := 1*3600 + 2*60 + 3.45
	if sec < want-0.01 || sec > want+0.01 {
		t.Fatalf("got %v want %v", sec, want)
	}
	if _, ok := ParseInputDuration("  Duration: N/A, start: 0.000000"); ok {
		t.Fatal("N/A should not parse")
	}
}

func TestProgressPercent(t *testing.T) {
	if got := ProgressPercent(30, 120); got < 24.9 || got > 25.1 {
		t.Fatalf("got %v", got)
	}
	if got := ProgressPercent(200, 100); got != 100 {
		t.Fatalf("clamp got %v", got)
	}
	if got := ProgressPercent(10, 0); got != 0 {
		t.Fatalf("unknown duration got %v", got)
	}
}

func ffmpegTestPath() string {
	if p := os.Getenv("FFMPEG_PATH"); p != "" {
		return p
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		return `c:\ffmpeg\bin\ffmpeg.exe`
	}
	return "/usr/bin/ffmpeg"
}

func TestStreamLinesCRProgress(t *testing.T) {
	r := strings.NewReader("frame=  10 fps=30\rframe=  20 fps=30\r\ndone\n")
	var got []string
	streamLines(r, func(line string) { got = append(got, line) })
	if len(got) < 2 {
		t.Fatalf("expected multiple progress lines, got %#v", got)
	}
}

func TestBuildArgsX265SpeedProfiles(t *testing.T) {
	cases := []struct {
		speed X265Speed
		want  []string
		not   []string
	}{
		{SpeedQuality, nil, []string{"rect=0", "rskip", "-x265-params"}},
		{"", nil, []string{"rect=0", "rskip", "-x265-params"}},
		{SpeedBalanced, []string{"rect=0:rskip=2:rskip-edge-threshold=2"}, []string{"limit-sao"}},
		{SpeedFast, []string{"rect=0", "rskip=2", "limit-sao=1"}, nil},
	}
	for _, c := range cases {
		joined := strings.Join(BuildArgs(Options{
			Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0},
			VideoCodec: CodecLibX265, CQ: 18, Speed: c.speed,
		}), " ")
		for _, w := range c.want {
			if !strings.Contains(joined, w) {
				t.Errorf("speed %q: expected %q in %s", c.speed, w, joined)
			}
		}
		for _, n := range c.not {
			if strings.Contains(joined, n) {
				t.Errorf("speed %q: unexpected %q in %s", c.speed, n, joined)
			}
		}
	}
}

func TestBuildArgsX265TuneSpeedAVX512Order(t *testing.T) {
	joined := strings.Join(BuildArgs(Options{
		Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0},
		VideoCodec: CodecLibX265, CQ: 18, Tune: "grain",
		Speed: SpeedBalanced, AVX512: true,
	}), " ")
	// tune must come first so the speed shortcuts override its defaults.
	want := "-x265-params tune=grain:rect=0:rskip=2:rskip-edge-threshold=2:asm=avx512"
	if !strings.Contains(joined, want) {
		t.Fatalf("expected %q, got: %s", want, joined)
	}
}

func TestBuildArgsX265HWDecode(t *testing.T) {
	opt := Options{
		Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0},
		VideoCodec: CodecLibX265, CQ: 18, HWDecode: true,
		ScaleHeight: 1080, Deinterlace: true,
	}
	args := BuildArgs(opt)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hwaccel cuda -i in.mkv") {
		t.Fatalf("expected NVDEC decode before input, got: %s", joined)
	}
	if strings.Contains(joined, "hwaccel_output_format") {
		t.Fatalf("frames must stay in system memory for CPU filters, got: %s", joined)
	}
	opt.HWDecode = false
	if joined := strings.Join(BuildArgs(opt), " "); strings.Contains(joined, "cuda") {
		t.Fatalf("no HWDecode must not use cuda, got: %s", joined)
	}
}

func TestBuildArgsNVENCTuned(t *testing.T) {
	opt := Options{
		Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0},
		VideoCodec: CodecHEVCNVENC, CQ: 20, PixFmt: "yuv420p10le",
	}
	joined := strings.Join(BuildArgs(opt), " ")
	if strings.Contains(joined, "-bf ") || strings.Contains(joined, "b_ref_mode") {
		t.Fatalf("untuned NVENC must not request B-frames, got: %s", joined)
	}
	if !strings.Contains(joined, "-profile:v main10 -pix_fmt p010le") {
		t.Fatalf("expected main10 profile for 10-bit NVENC, got: %s", joined)
	}
	opt.NVENCTuned = true
	joined = strings.Join(BuildArgs(opt), " ")
	for _, w := range []string{"-tune hq", "-rc-lookahead 32", "-bf 4", "-b_ref_mode middle", "-multipass fullres"} {
		if !strings.Contains(joined, w) {
			t.Fatalf("expected %q in tuned NVENC args, got: %s", w, joined)
		}
	}
}

func TestHardwareProbesLive(t *testing.T) {
	path := ffmpegTestPath()
	if _, err := os.Stat(path); err != nil {
		t.Skip("ffmpeg not available")
	}
	t.Logf("HasCUDA = %v, HasNVENCTuned = %v", HasCUDA(path), HasNVENCTuned(path))
	if runProbe("") {
		t.Fatal("empty ffmpeg path must not probe as available")
	}
}

func TestBuildArgsSVTAV1(t *testing.T) {
	joined := strings.Join(BuildArgs(Options{
		Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0},
		VideoCodec: CodecSVTAV1, CQ: 15, HWDecode: true,
		Speed: SpeedFast, AVX512: true, // x265-only knobs must not leak
	}), " ")
	for _, w := range []string{"-hwaccel cuda -i in.mkv", "-c:v libsvtav1 -crf 15 -preset 5 -pix_fmt yuv420p10le", "-svtav1-params tune=0"} {
		if !strings.Contains(joined, w) {
			t.Fatalf("expected %q, got: %s", w, joined)
		}
	}
	if strings.Contains(joined, "x265") || strings.Contains(joined, "rskip") {
		t.Fatalf("x265 options leaked into AV1 args: %s", joined)
	}
	if SuggestedAV1CRF(1920, 1080) != 15 || DefaultPreset(CodecSVTAV1) != "5" {
		t.Fatalf("unexpected AV1 defaults: crf %d preset %s", SuggestedAV1CRF(1920, 1080), DefaultPreset(CodecSVTAV1))
	}
}

func TestBuildArgsHDR10Metadata(t *testing.T) {
	md := "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"
	base := Options{
		Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0}, CQ: 18,
		PixFmt: "yuv420p10le", HDR: true, ColorTransfer: "smpte2084",
		ColorPrimaries: "bt2020", ColorSpace: "bt2020nc",
		MasterDisplay: md, MaxCLL: "1000,400",
	}
	base.VideoCodec = CodecLibX265
	x265 := strings.Join(BuildArgs(base), " ")
	if !strings.Contains(x265, ":master-display="+md+":max-cll=1000,400") {
		t.Fatalf("x265 must get explicit HDR10 metadata, got: %s", x265)
	}
	base.VideoCodec = CodecSVTAV1
	av1 := strings.Join(BuildArgs(base), " ")
	wantMD := "mastering-display=G(0.2650,0.6900)B(0.1500,0.0600)R(0.6800,0.3200)WP(0.3127,0.3290)L(1000.0000,0.0050)"
	if !strings.Contains(av1, wantMD) || !strings.Contains(av1, "content-light=1000,400") {
		t.Fatalf("AV1 must get HDR10 metadata in SVT syntax, got: %s", av1)
	}
	if !strings.Contains(av1, "-color_trc smpte2084") {
		t.Fatalf("AV1 must tag PQ transfer, got: %s", av1)
	}
	base.HDR = false
	base.VideoCodec = CodecLibX265
	if sdr := strings.Join(BuildArgs(base), " "); strings.Contains(sdr, "master-display") {
		t.Fatalf("SDR encode must not get HDR10 metadata, got: %s", sdr)
	}
}

func TestBuildArgsNVENCGPUFilters(t *testing.T) {
	opt := Options{
		Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0},
		VideoCodec: CodecHEVCNVENC, CQ: 20, PixFmt: "yuv420p10le",
		ScaleHeight: 1080, Deinterlace: true, GPUFilters: true,
	}
	joined := strings.Join(BuildArgs(opt), " ")
	for _, w := range []string{
		"-hwaccel cuda -hwaccel_output_format cuda -i in.mkv",
		"-vf bwdif_cuda=mode=send_field,scale_cuda=w=-2:h='min(ih,1080)':interp_algo=lanczos:format=p010le",
		"-profile:v main10",
	} {
		if !strings.Contains(joined, w) {
			t.Fatalf("expected %q, got: %s", w, joined)
		}
	}
	if strings.Contains(joined, "-pix_fmt") {
		t.Fatalf("CUDA frames must not get a CPU -pix_fmt conversion, got: %s", joined)
	}

	// Without the probe's blessing the CPU filter path stays as before.
	opt.GPUFilters = false
	joined = strings.Join(BuildArgs(opt), " ")
	if strings.Contains(joined, "cuda") || !strings.Contains(joined, "-vf bwdif,scale=") {
		t.Fatalf("expected CPU filters without cuda, got: %s", joined)
	}

	// GPUFilters without any filter keeps the plain NVDEC path.
	opt = Options{Input: "in.mkv", Output: "out.mkv", VideoMaps: []int{0},
		VideoCodec: CodecHEVCNVENC, CQ: 20, GPUFilters: true}
	joined = strings.Join(BuildArgs(opt), " ")
	if strings.Contains(joined, "hwaccel_output_format") || strings.Contains(joined, "-vf") {
		t.Fatalf("no filters: expected plain -hwaccel cuda, got: %s", joined)
	}
}

func TestSVTMasteringDisplayRejectsGarbage(t *testing.T) {
	if got := svtMasteringDisplay("G(1,2)B(3,4)"); got != "" {
		t.Fatalf("expected empty result for malformed input, got %q", got)
	}
}

func TestCUDAVideoFilterVariants(t *testing.T) {
	cases := []struct {
		opt  Options
		want string
	}{
		{Options{Deinterlace: true}, "bwdif_cuda=mode=send_field"},
		{Options{Deinterlace: true, PixFmt: "yuv420p10le"}, "bwdif_cuda=mode=send_field,scale_cuda=format=p010le"},
		{Options{ScaleHeight: 720, PixFmt: "yuv420p"}, "scale_cuda=w=-2:h='min(ih,720)':interp_algo=lanczos:format=yuv420p"},
		{Options{PixFmt: "yuv420p10le"}, ""},
	}
	for _, c := range cases {
		if got := cudaVideoFilter(c.opt); got != c.want {
			t.Errorf("cudaVideoFilter(%+v) = %q, want %q", c.opt, got, c.want)
		}
	}
}

func TestPositionTrackerFallsBackToFrames(t *testing.T) {
	var p PositionTracker
	lines := []string{
		"  Stream #0:0(eng): Video: hevc (Main 10), yuv420p10le(tv, bt2020nc/bt2020/smpte2084), 3840x2160, SAR 1:1 DAR 16:9, 23.98 fps, 23.98 tbr, 1k tbn (default)",
		"  Stream #0:5: Video: mjpeg (Baseline), yuvj420p, 600x900, 90k tbr, 90k tbn (attached pic)",
		"Output #0, matroska, to 'out.mkv':",
		"  Stream #0:0(eng): Video: hevc (Main 10), p010le(tv, bt2020nc/bt2020/smpte2084, progressive), 3840x2160, q=2-31, 47.95 fps, 1k tbn (default)",
	}
	for _, l := range lines {
		if _, ok := p.Position(l); ok {
			t.Fatalf("banner line reported a position: %q", l)
		}
	}
	sec, ok := p.Position("frame= 3473 fps= 32 q=22.0 q=0.0 size=  283648KiB time=N/A bitrate=N/A speed=N/A elapsed=0:01:47.52")
	if !ok || math.Abs(sec-3473/47.95) > 1e-9 {
		t.Fatalf("got %v %v, want %v from the output fps", sec, ok, 3473/47.95)
	}
	sec, ok = p.Position("frame=  100 fps= 32 q=22.0 size=  1024KiB time=00:00:04.17 bitrate=1.0kbits/s speed=1.3x")
	if !ok || math.Abs(sec-4.17) > 1e-9 {
		t.Fatalf("time= should win: got %v %v", sec, ok)
	}
}

func TestPositionTrackerUsesInputFPSWithoutOutputRate(t *testing.T) {
	var p PositionTracker
	p.Position("  Stream #0:0: Video: h264 (High), yuv420p, 1920x1080, 25 fps, 25 tbr, 1k tbn")
	p.Position("Output #0, matroska, to 'out.mkv':")
	sec, ok := p.Position("frame=  250 fps=50 q=-1.0 size=  1KiB time=N/A bitrate=N/A speed=N/A")
	if !ok || sec != 10 {
		t.Fatalf("got %v %v, want 10", sec, ok)
	}
	if _, ok := p.Position("frame=  250 something else"); ok {
		t.Fatal("non-stats line must not report a position")
	}
}

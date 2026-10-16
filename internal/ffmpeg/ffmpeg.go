package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"enkoder/internal/procattr"
)

type VideoCodec string

const (
	CodecHEVCNVENC VideoCodec = "hevc_nvenc"
	CodecLibX265   VideoCodec = "libx265"
	CodecSVTAV1    VideoCodec = "libsvtav1"
	CodecCopy      VideoCodec = "copy"
)

// X265Speed selects extra x265 analysis shortcuts on top of the preset.
// Values were picked from benchmarks on 1080p BluRay sources: balanced is
// about 2x faster than plain preset slow at near-identical size and SSIM.
type X265Speed string

const (
	SpeedQuality  X265Speed = "quality"
	SpeedBalanced X265Speed = "balanced"
	SpeedFast     X265Speed = "fast"
)

// Options matches encode-advisor quality knobs where they fit this GUI:
// libx265 preset/CRF/tune/pix_fmt/HDR x265-params, optional bwdif,
// plus NVENC equivalents (preset/CQ/pix_fmt/color tags).
type Options struct {
	Input        string
	Output       string
	VideoMaps    []int
	AudioMaps    []int
	SubtitleMaps []int
	VideoCodec   VideoCodec
	Container    string
	// CQ is CRF (libx265) or CQ (NVENC). 0 is valid (maximum quality).
	// Negative values mean “use default 18”.
	CQ int
	// Preset: libx265 ultrafast…placebo (default slow); NVENC p1…p7 (default p7).
	Preset string
	// Tune is libx265 only: film, animation, grain, or empty.
	Tune string
	// PixFmt: yuv420p / yuv420p10le. For NVENC, 10-bit is rewritten to p010le.
	PixFmt string
	// ScaleHeight is target max height in pixels (e.g. 1080). 0 = keep source.
	// Only downscales (never upscales). Incompatible with CodecCopy.
	ScaleHeight    int
	Deinterlace    bool
	HDR            bool
	ColorTransfer  string
	ColorPrimaries string
	ColorSpace     string
	CopyAudio      bool
	CopySubs       bool
	MapMetadata    bool
	MapChapters    bool
	// Speed is libx265 only; empty means SpeedQuality.
	Speed X265Speed
	// AVX512 forces x265 to use its AVX-512 kernels (off by default in x265).
	AVX512 bool
	// HWDecode decodes the source on the GPU (NVDEC) for libx265 encodes,
	// freeing CPU for the encoder. Frames are downloaded to system memory,
	// so CPU filters keep working; unsupported codecs fall back to software.
	HWDecode bool
	// NVENCTuned adds B-frame references, lookahead and multipass to NVENC.
	// Only set when the GPU accepted these options (see HasNVENCTuned).
	NVENCTuned bool
	// GPUFilters keeps NVENC encodes fully on the GPU: frames stay in CUDA
	// memory and scale/deinterlace use scale_cuda/bwdif_cuda. Only set after
	// CanGPUFilter confirmed NVDEC decodes this input.
	GPUFilters bool
	// MasterDisplay (x265 master-display syntax) and MaxCLL ("cll,fall")
	// are explicit HDR10 static metadata. ffmpeg >= 7.1 copies the source's
	// metadata by itself; these keep older builds correct too.
	MasterDisplay string
	MaxCLL        string
}

type ProgressFunc func(line string)

// RunOptions configures process lifecycle hooks for an encode job.
type RunOptions struct {
	OnLine  ProgressFunc
	OnStart func(cmd *exec.Cmd) // called after Start, before Wait
}

var (
	nvencOnce sync.Once
	nvencOK   bool

	cudaOnce sync.Once
	cudaOK   bool

	nvencTunedOnce sync.Once
	nvencTunedOK   bool

	svtOnce sync.Once
	svtOK   bool
)

// HasHEVCNVENC reports whether ffmpeg lists the hevc_nvenc encoder.
// Result is cached for the process lifetime.
func HasHEVCNVENC(ffmpegPath string) bool {
	nvencOnce.Do(func() {
		nvencOK = detectEncoder(ffmpegPath, "hevc_nvenc")
	})
	return nvencOK
}

// HasSVTAV1 reports whether ffmpeg lists the libsvtav1 encoder. Cached.
func HasSVTAV1(ffmpegPath string) bool {
	svtOnce.Do(func() {
		svtOK = detectEncoder(ffmpegPath, "libsvtav1")
	})
	return svtOK
}

// ResetNVENCCache clears the cached detection result (tests / re-check).
func ResetNVENCCache() {
	nvencOnce = sync.Once{}
	nvencOK = false
}

func detectEncoder(ffmpegPath, name string) bool {
	if strings.TrimSpace(ffmpegPath) == "" {
		return false
	}
	cmd := exec.Command(ffmpegPath, "-hide_banner", "-encoders")
	procattr.HideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		for _, f := range fields {
			if f == name {
				return true
			}
		}
	}
	return false
}

// nvencTunedArgs are the NVENC options beyond the original preset/CQ/AQ set.
// Pascal GPUs reject HEVC B-frames, so HasNVENCTuned probes them first.
var nvencTunedArgs = []string{
	"-tune", "hq",
	"-rc-lookahead", "32",
	"-bf", "4",
	"-b_ref_mode", "middle",
	"-multipass", "fullres",
}

// HasCUDA reports whether ffmpeg can open a CUDA device (a working NVIDIA
// GPU and driver, not just a CUDA-enabled ffmpeg build). Cached.
func HasCUDA(ffmpegPath string) bool {
	cudaOnce.Do(func() {
		cudaOK = runProbe(ffmpegPath,
			"-init_hw_device", "cuda=cu",
			"-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.04",
			"-f", "null", "-")
	})
	return cudaOK
}

// HasNVENCTuned reports whether a short hevc_nvenc encode with
// nvencTunedArgs succeeds on this GPU. Cached.
func HasNVENCTuned(ffmpegPath string) bool {
	nvencTunedOnce.Do(func() {
		if !HasHEVCNVENC(ffmpegPath) {
			return
		}
		args := []string{"-f", "lavfi", "-i", "testsrc2=s=320x240:r=25:d=0.2",
			"-c:v", "hevc_nvenc", "-preset", "p7", "-rc", "vbr", "-cq", "20", "-b:v", "0"}
		args = append(args, nvencTunedArgs...)
		args = append(args, "-f", "null", "-")
		nvencTunedOK = runProbe(ffmpegPath, args...)
	})
	return nvencTunedOK
}

// CanGPUFilter reports whether NVDEC decodes input and the CUDA filter chain
// for opt runs on it. NVDEC silently falls back to software decoding for
// unsupported sources (e.g. 4:2:2 H.264, MPEG-4 ASP), and CUDA filters then
// fail, so this decodes and filters one real frame before committing.
func CanGPUFilter(ffmpegPath string, opt Options) bool {
	vf := cudaVideoFilter(opt)
	if vf == "" || len(opt.VideoMaps) == 0 {
		return false
	}
	return runProbe(ffmpegPath,
		"-hwaccel", "cuda", "-hwaccel_output_format", "cuda",
		"-i", opt.Input,
		"-map", fmt.Sprintf("0:%d", opt.VideoMaps[0]),
		"-vf", vf, "-frames:v", "1", "-f", "null", "-")
}

func runProbe(ffmpegPath string, args ...string) bool {
	if strings.TrimSpace(ffmpegPath) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegPath, append([]string{"-v", "error", "-hide_banner"}, args...)...)
	procattr.HideConsole(cmd)
	return cmd.Run() == nil
}

// SuggestedAV1CRF is the SVT-AV1 CRF that roughly matches SuggestedCRF's
// x265 quality (AV1 CRF runs 0–63, so the numbers differ).
func SuggestedAV1CRF(width, height int) int {
	return SuggestedCRF(width, height) + AV1CRFOffset
}

// DefaultVideoCodec returns hevc_nvenc when available, otherwise libx265.
func DefaultVideoCodec(ffmpegPath string) VideoCodec {
	if HasHEVCNVENC(ffmpegPath) {
		return CodecHEVCNVENC
	}
	return CodecLibX265
}

// SuggestedCRF mirrors encode-advisor tier defaults (quality-first).
func SuggestedCRF(width, height int) int {
	longest := width
	if height > longest {
		longest = height
	}
	switch {
	case longest >= 3000:
		return 20
	case longest >= 1400:
		return 18
	case longest >= 1000:
		return 18
	default:
		return 17
	}
}

// SuggestedPixFmt mirrors encode-advisor: 10-bit for HDR or >8-bit sources.
func SuggestedPixFmt(hdr bool, bitsPerSample int) string {
	if hdr || bitsPerSample > 8 {
		return "yuv420p10le"
	}
	return "yuv420p"
}

// DefaultPreset returns encode-advisor's slow for libx265, p7 for NVENC.
func DefaultPreset(codec VideoCodec) string {
	switch codec {
	case CodecHEVCNVENC:
		return "p7"
	case CodecSVTAV1:
		return "5"
	}
	return "slow"
}

func qualityOrDefault(q int) int {
	if q < 0 {
		return 18
	}
	return q
}

func BuildArgs(opt Options) []string {
	args := []string{"-y", "-hide_banner", "-stats"}

	// CUDA hwaccel + software scale/deinterlace is unreliable; skip when filtering.
	needFilter := opt.ScaleHeight > 0 || opt.Deinterlace
	cpuEncoder := opt.VideoCodec == CodecLibX265 || opt.VideoCodec == CodecSVTAV1
	switch {
	case opt.VideoCodec == CodecHEVCNVENC && opt.GPUFilters && needFilter:
		args = append(args, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
	case opt.VideoCodec == CodecHEVCNVENC && !needFilter:
		args = append(args, "-hwaccel", "cuda")
	case cpuEncoder && opt.HWDecode:
		args = append(args, "-hwaccel", "cuda")
	}
	args = append(args, "-i", opt.Input)

	for _, idx := range opt.VideoMaps {
		args = append(args, "-map", fmt.Sprintf("0:%d", idx))
	}
	for _, idx := range opt.AudioMaps {
		args = append(args, "-map", fmt.Sprintf("0:%d", idx))
	}
	for _, idx := range opt.SubtitleMaps {
		args = append(args, "-map", fmt.Sprintf("0:%d", idx))
	}

	if vf := videoFilter(opt); vf != "" {
		args = append(args, "-vf", vf)
	}

	switch opt.VideoCodec {
	case CodecCopy:
		args = append(args, "-c:v", "copy")
	case CodecLibX265:
		args = append(args, libx265Args(opt)...)
	case CodecSVTAV1:
		args = append(args, svtav1Args(opt)...)
	default:
		args = append(args, nvencArgs(opt)...)
	}

	if opt.CopyAudio || len(opt.AudioMaps) > 0 {
		args = append(args, "-c:a", "copy")
	}
	if opt.CopySubs || len(opt.SubtitleMaps) > 0 {
		args = append(args, "-c:s", "copy")
	}

	// encode-advisor always keeps container metadata and chapters on re-encode.
	if opt.MapMetadata {
		args = append(args, "-map_metadata", "0")
	}
	if opt.MapChapters {
		args = append(args, "-map_chapters", "0")
	}

	args = append(args, opt.Output)
	return args
}

func videoFilter(opt Options) string {
	if opt.VideoCodec == CodecHEVCNVENC && opt.GPUFilters {
		return cudaVideoFilter(opt)
	}
	var parts []string
	if opt.Deinterlace {
		parts = append(parts, "bwdif")
	}
	if opt.ScaleHeight > 0 && opt.VideoCodec != CodecCopy {
		// Downscale only: never enlarge smaller sources.
		parts = append(parts, fmt.Sprintf("scale=-2:min(ih\\,%d):flags=lanczos", opt.ScaleHeight))
	}
	return strings.Join(parts, ",")
}

// cudaVideoFilter mirrors videoFilter on CUDA frames. bwdif_cuda defaults to
// send_frame, unlike bwdif, so the mode is pinned to keep 25i -> 50p. The
// pixel format conversion happens in scale_cuda because NVENC cannot
// convert CUDA frames itself.
func cudaVideoFilter(opt Options) string {
	if opt.ScaleHeight <= 0 && !opt.Deinterlace {
		return ""
	}
	var parts []string
	if opt.Deinterlace {
		parts = append(parts, "bwdif_cuda=mode=send_field")
	}
	var scale []string
	if opt.ScaleHeight > 0 {
		scale = append(scale, "w=-2", fmt.Sprintf("h='min(ih,%d)'", opt.ScaleHeight), "interp_algo=lanczos")
	}
	switch strings.TrimSpace(opt.PixFmt) {
	case "yuv420p10le", "p010le":
		scale = append(scale, "format=p010le")
	case "yuv420p":
		scale = append(scale, "format=yuv420p")
	}
	if len(scale) > 0 {
		parts = append(parts, "scale_cuda="+strings.Join(scale, ":"))
	}
	return strings.Join(parts, ",")
}

func libx265Args(opt Options) []string {
	crf := qualityOrDefault(opt.CQ)
	preset := strings.TrimSpace(opt.Preset)
	if preset == "" {
		preset = DefaultPreset(CodecLibX265)
	}
	args := []string{"-c:v", "libx265", "-crf", strconv.Itoa(crf), "-preset", preset}
	pix := strings.TrimSpace(opt.PixFmt)
	if pix == "" {
		pix = "yuv420p"
	}
	args = append(args, "-pix_fmt", pix)
	if params := x265Params(opt); params != "" {
		args = append(args, "-x265-params", params)
	}
	return args
}

func nvencArgs(opt Options) []string {
	cq := qualityOrDefault(opt.CQ)
	preset := strings.TrimSpace(opt.Preset)
	if preset == "" {
		preset = DefaultPreset(CodecHEVCNVENC)
	}
	args := []string{
		"-c:v", "hevc_nvenc",
		"-preset", preset,
		"-rc", "vbr",
		"-cq", strconv.Itoa(cq),
		"-b:v", "0",
		"-spatial-aq", "1",
		"-temporal-aq", "1",
	}
	if opt.NVENCTuned {
		args = append(args, nvencTunedArgs...)
	}
	pix := strings.TrimSpace(opt.PixFmt)
	if opt.GPUFilters && (opt.ScaleHeight > 0 || opt.Deinterlace) {
		// scale_cuda already emits the target format on the GPU.
		if pix == "yuv420p10le" || pix == "p010le" {
			args = append(args, "-profile:v", "main10")
		}
		return append(args, colorTagArgs(opt)...)
	}
	switch pix {
	case "yuv420p10le", "p010le":
		args = append(args, "-profile:v", "main10", "-pix_fmt", "p010le")
	case "yuv420p", "":
		// leave encoder default / 8-bit
		if pix == "yuv420p" {
			args = append(args, "-pix_fmt", "yuv420p")
		}
	default:
		args = append(args, "-pix_fmt", pix)
	}
	args = append(args, colorTagArgs(opt)...)
	return args
}

func x265Params(opt Options) string {
	var parts []string
	tune := strings.TrimSpace(opt.Tune)
	if tune != "" {
		parts = append(parts, "tune="+tune)
	}
	parts = append(parts, speedParams(opt.Speed)...)
	if opt.AVX512 {
		parts = append(parts, "asm=avx512")
	}
	transfer := strings.ToLower(strings.TrimSpace(opt.ColorTransfer))
	if opt.HDR && transfer == "smpte2084" {
		parts = append(parts,
			"hdr-opt=1",
			"repeat-headers=1",
			"colorprim=bt2020",
			"transfer=smpte2084",
			"colormatrix=bt2020nc",
		)
		if opt.MasterDisplay != "" {
			parts = append(parts, "master-display="+opt.MasterDisplay)
		}
		if opt.MaxCLL != "" {
			parts = append(parts, "max-cll="+opt.MaxCLL)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ":")
}

// AV1CRFOffset maps x265 CRF to SVT-AV1 CRF at similar quality. Measured on
// 1080p BluRay: x265 slow CRF 18 gives SSIM 0.9737; SVT-AV1 preset 5 gives
// 0.9752 at CRF 14 and 0.9712 at CRF 17 with 35% smaller files.
const AV1CRFOffset = -3

func svtav1Args(opt Options) []string {
	crf := qualityOrDefault(opt.CQ)
	preset := strings.TrimSpace(opt.Preset)
	if preset == "" {
		preset = DefaultPreset(CodecSVTAV1)
	}
	pix := strings.TrimSpace(opt.PixFmt)
	if pix == "" {
		// 10-bit AV1 compresses better even for 8-bit sources.
		pix = "yuv420p10le"
	}
	args := []string{"-c:v", "libsvtav1", "-crf", strconv.Itoa(crf), "-preset", preset, "-pix_fmt", pix}
	// tune=0 is SVT's visual-quality tune (default 1 optimises PSNR).
	params := []string{"tune=0"}
	transfer := strings.ToLower(strings.TrimSpace(opt.ColorTransfer))
	if opt.HDR && transfer == "smpte2084" {
		if md := svtMasteringDisplay(opt.MasterDisplay); md != "" {
			params = append(params, "mastering-display="+md)
		}
		if opt.MaxCLL != "" {
			params = append(params, "content-light="+opt.MaxCLL)
		}
	}
	args = append(args, "-svtav1-params", strings.Join(params, ":"))
	return append(args, colorTagArgs(opt)...)
}

// svtMasteringDisplay converts x265's integer master-display syntax to
// SVT-AV1's decimal one: G(x,y)B(x,y)R(x,y)WP(x,y)L(max,min).
func svtMasteringDisplay(x265 string) string {
	var v [10]int64
	n, err := fmt.Sscanf(x265, "G(%d,%d)B(%d,%d)R(%d,%d)WP(%d,%d)L(%d,%d)",
		&v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7], &v[8], &v[9])
	if err != nil || n != 10 {
		return ""
	}
	c := func(i int) string { return strconv.FormatFloat(float64(v[i])/50000, 'f', 4, 64) }
	l := func(i int) string { return strconv.FormatFloat(float64(v[i])/10000, 'f', 4, 64) }
	return fmt.Sprintf("G(%s,%s)B(%s,%s)R(%s,%s)WP(%s,%s)L(%s,%s)",
		c(0), c(1), c(2), c(3), c(4), c(5), c(6), c(7), l(8), l(9))
}

// speedParams returns x265-params shortcuts for a speed profile.
// rect=0 skips rectangular partitions (~1.5x, +2% size); rskip=2 is the
// edge-density early CU skip (~1.6x on top); fast adds cheaper SAO, hex
// motion search and 3 refs for ~1.2x more at about +3% size.
func speedParams(speed X265Speed) []string {
	switch speed {
	case SpeedBalanced:
		return []string{"rect=0", "rskip=2", "rskip-edge-threshold=2"}
	case SpeedFast:
		return []string{"rect=0", "rskip=2", "rskip-edge-threshold=2",
			"limit-sao=1", "me=hex", "subme=2", "ref=3"}
	default:
		return nil
	}
}

func colorTagArgs(opt Options) []string {
	var args []string
	if p := strings.TrimSpace(opt.ColorPrimaries); p != "" {
		args = append(args, "-color_primaries", p)
	}
	if t := strings.TrimSpace(opt.ColorTransfer); t != "" {
		args = append(args, "-color_trc", t)
	}
	if s := strings.TrimSpace(opt.ColorSpace); s != "" {
		args = append(args, "-colorspace", s)
	}
	return args
}

// ParseStatsTime extracts playback position in seconds from an ffmpeg -stats line.
// Returns ok=false when the line has no time= field.
func ParseStatsTime(line string) (sec float64, ok bool) {
	return parseHMSField(line, "time=")
}

// ParseInputDuration extracts media duration from an ffmpeg banner line like
// "Duration: 01:02:03.45, start: 0.000000, bitrate: ...".
func ParseInputDuration(line string) (sec float64, ok bool) {
	return parseHMSField(line, "Duration:")
}

// ProgressPercent is current/duration*100, clamped to [0,100].
// Returns 0 when duration is unknown/non-positive.
func ProgressPercent(current, duration float64) float64 {
	if duration <= 0 || current < 0 || !isFinite(current) || !isFinite(duration) {
		return 0
	}
	pct := current / duration * 100
	if pct > 100 {
		return 100
	}
	return pct
}

func parseHMSField(line, key string) (sec float64, ok bool) {
	i := strings.Index(line, key)
	if i < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(line[i+len(key):])
	if rest == "" || strings.HasPrefix(rest, "N/A") {
		return 0, false
	}
	end := 0
	for end < len(rest) {
		c := rest[end]
		if (c >= '0' && c <= '9') || c == ':' || c == '.' {
			end++
			continue
		}
		break
	}
	return parseHMS(rest[:end])
}

func parseHMS(raw string) (sec float64, ok bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.ParseFloat(parts[0], 64)
	m, err2 := strconv.ParseFloat(parts[1], 64)
	s, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return h*3600 + m*60 + s, true
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// Run starts ffmpeg and waits until it exits or ctx is cancelled.
// On cancel it kills the process explicitly (needed on Windows).
func Run(ctx context.Context, ffmpegPath string, opt Options, ro RunOptions) error {
	args := BuildArgs(opt)
	cmd := exec.Command(ffmpegPath, args...)
	procattr.HideConsole(cmd)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if ro.OnStart != nil {
		ro.OnStart(cmd)
	}

	doneRead := make(chan struct{})
	go func() {
		streamLines(stderr, ro.OnLine)
		close(doneRead)
	}()

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()

	var errOut error
	select {
	case <-ctx.Done():
		killProcess(cmd)
		errOut = ctx.Err()
		select {
		case <-waitErr:
		case <-time.After(5 * time.Second):
		}
	case errOut = <-waitErr:
	}

	<-doneRead

	if errOut != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg failed: %w\ncommand: %s %s", errOut, ffmpegPath, strings.Join(args, " "))
	}
	return nil
}

func killProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// scanFFmpegLines splits on \n or \r so progress updates (CR-rewritten) reach the UI.
func scanFFmpegLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		adv := i + 1
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			adv = i + 2
		} else if data[i] == '\n' && i+1 < len(data) && data[i+1] == '\r' {
			adv = i + 2
		}
		return adv, bytes.TrimRight(data[:i], "\r\n"), nil
	}
	if atEOF {
		return len(data), bytes.TrimRight(data, "\r\n"), nil
	}
	return 0, nil, nil
}

func streamLines(r io.Reader, onLine ProgressFunc) {
	if onLine == nil {
		io.Copy(io.Discard, r)
		return
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sc.Split(scanFFmpegLines)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		onLine(line)
	}
}

func CommandPreview(ffmpegPath string, opt Options) string {
	args := BuildArgs(opt)
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, quote(ffmpegPath))
	for _, a := range args {
		quoted = append(quoted, quote(a))
	}
	return strings.Join(quoted, " ")
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

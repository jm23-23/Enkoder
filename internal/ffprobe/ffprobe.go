package ffprobe

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"enkoder/internal/procattr"
)

type ProbeResult struct {
	Streams []Stream `json:"streams"`
	Format  Format   `json:"format"`
}

type Format struct {
	Filename       string `json:"filename"`
	Duration       string `json:"duration"`
	BitRate        string `json:"bit_rate"`
	FormatLongName string `json:"format_long_name"`
}

type Stream struct {
	Index            int               `json:"index"`
	CodecType        string            `json:"codec_type"`
	CodecName        string            `json:"codec_name"`
	CodecLongName    string            `json:"codec_long_name"`
	Profile          string            `json:"profile"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	PixFmt           string            `json:"pix_fmt"`
	AvgFrameRate     string            `json:"avg_frame_rate"`
	FieldOrder       string            `json:"field_order"`
	ColorTransfer    string            `json:"color_transfer"`
	ColorPrimaries   string            `json:"color_primaries"`
	ColorSpace       string            `json:"color_space"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	SampleRate       string            `json:"sample_rate"`
	BitRate          string            `json:"bit_rate"`
	Duration         string            `json:"duration"`
	Tags             map[string]string `json:"tags"`
	Disposition      map[string]int    `json:"disposition"`
	SideDataList     []json.RawMessage `json:"side_data_list"`
}

// VideoQuality summarizes encode-advisor-style hints from the first video stream.
type VideoQuality struct {
	Width          int
	Height         int
	BitsPerSample  int
	HDR            bool
	DolbyVision    bool
	Interlaced     bool
	ColorTransfer  string
	ColorPrimaries string
	ColorSpace     string
	PixFmt         string
}

func (s Stream) Language() string {
	if s.Tags == nil {
		return ""
	}
	for _, key := range []string{"language", "LANGUAGE", "lang"} {
		if v := strings.TrimSpace(s.Tags[key]); v != "" {
			return v
		}
	}
	return ""
}

func (s Stream) Title() string {
	if s.Tags == nil {
		return ""
	}
	return strings.TrimSpace(s.Tags["title"])
}

func (s Stream) Label() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("#%d", s.Index))
	parts = append(parts, strings.ToUpper(s.CodecType))
	if s.CodecName != "" {
		parts = append(parts, s.CodecName)
	}
	if lang := s.Language(); lang != "" {
		parts = append(parts, lang)
	}
	if title := s.Title(); title != "" {
		parts = append(parts, `"`+title+`"`)
	}
	switch s.CodecType {
	case "video":
		if s.Width > 0 && s.Height > 0 {
			parts = append(parts, fmt.Sprintf("%dx%d", s.Width, s.Height))
		}
		if s.AvgFrameRate != "" && s.AvgFrameRate != "0/0" {
			parts = append(parts, s.AvgFrameRate+" fps")
		}
		if bits := s.BitsPerSample(); bits > 8 {
			parts = append(parts, fmt.Sprintf("%d-bit", bits))
		}
		if s.IsHDR() {
			parts = append(parts, "HDR")
		}
	case "audio":
		if s.ChannelLayout != "" {
			parts = append(parts, s.ChannelLayout)
		} else if s.Channels > 0 {
			parts = append(parts, fmt.Sprintf("%dch", s.Channels))
		}
		if br := formatBitrate(s.BitRate); br != "" {
			parts = append(parts, br)
		}
	}
	return strings.Join(parts, " · ")
}

// BitsPerSample mirrors encode-advisor._bits.
func (s Stream) BitsPerSample() int {
	pix := strings.ToLower(s.PixFmt)
	if strings.Contains(pix, "10") {
		return 10
	}
	if strings.Contains(pix, "12") {
		return 12
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s.BitsPerRawSample)); err == nil && n > 0 {
		return n
	}
	return 8
}

// IsHDR mirrors encode-advisor._is_hdr (PQ/HLG or mastering display / CLL side data).
func (s Stream) IsHDR() bool {
	transfer := strings.ToLower(strings.TrimSpace(s.ColorTransfer))
	if transfer == "smpte2084" || transfer == "arib-std-b67" {
		return true
	}
	blob := strings.ToLower(string(bytesJoin(s.SideDataList)))
	return strings.Contains(blob, "mastering display") || strings.Contains(blob, "content light")
}

// IsDolbyVision mirrors encode-advisor._is_dovi.
func (s Stream) IsDolbyVision() bool {
	blob := strings.ToLower(s.CodecName + " " + s.CodecLongName + " " + string(bytesJoin(s.SideDataList)))
	return strings.Contains(blob, "dolby vision") || strings.Contains(blob, "dovi")
}

// IsInterlaced mirrors encode-advisor interlaced check.
func (s Stream) IsInterlaced() bool {
	fo := strings.ToLower(strings.TrimSpace(s.FieldOrder))
	return fo != "" && fo != "progressive" && fo != "unknown"
}

func FirstVideoQuality(res *ProbeResult) VideoQuality {
	if res == nil {
		return VideoQuality{}
	}
	for _, s := range res.Streams {
		if s.CodecType != "video" {
			continue
		}
		return VideoQuality{
			Width:          s.Width,
			Height:         s.Height,
			BitsPerSample:  s.BitsPerSample(),
			HDR:            s.IsHDR(),
			DolbyVision:    s.IsDolbyVision(),
			Interlaced:     s.IsInterlaced(),
			ColorTransfer:  s.ColorTransfer,
			ColorPrimaries: s.ColorPrimaries,
			ColorSpace:     s.ColorSpace,
			PixFmt:         s.PixFmt,
		}
	}
	return VideoQuality{}
}

func bytesJoin(parts []json.RawMessage) []byte {
	if len(parts) == 0 {
		return nil
	}
	var b strings.Builder
	for _, p := range parts {
		b.Write(p)
		b.WriteByte(' ')
	}
	return []byte(b.String())
}

func formatBitrate(raw string) string {
	if raw == "" {
		return ""
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f kb/s", n/1000)
}

func Probe(ffprobePath, input string) (*ProbeResult, error) {
	cmd := exec.Command(ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		input,
	)
	procattr.HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var res ProbeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}
	return &res, nil
}

// DurationSeconds returns the best-known media duration from format and streams.
// Some containers omit format.duration but still expose stream duration or
// Tags["DURATION"] (common for MKV from MakeMKV / mkvmerge).
func DurationSeconds(res *ProbeResult) float64 {
	if res == nil {
		return 0
	}
	best := parseFloatDuration(res.Format.Duration)
	for _, s := range res.Streams {
		if d := parseFloatDuration(s.Duration); d > best {
			best = d
		}
		if s.Tags != nil {
			for _, key := range []string{"DURATION", "Duration", "duration"} {
				if d := parseTagDuration(s.Tags[key]); d > best {
					best = d
				}
			}
		}
	}
	return best
}

func parseFloatDuration(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "N/A") {
		return 0
	}
	sec, err := strconv.ParseFloat(raw, 64)
	if err != nil || sec < 0 {
		return 0
	}
	return sec
}

// parseTagDuration accepts seconds ("123.45") or clock ("01:02:03.45").
func parseTagDuration(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "N/A") {
		return 0
	}
	if strings.Contains(raw, ":") {
		parts := strings.Split(raw, ":")
		if len(parts) != 3 {
			return 0
		}
		h, err1 := strconv.ParseFloat(parts[0], 64)
		m, err2 := strconv.ParseFloat(parts[1], 64)
		s, err3 := strconv.ParseFloat(parts[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return 0
		}
		return h*3600 + m*60 + s
	}
	return parseFloatDuration(raw)
}

package ffprobe

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"enkoder/internal/procattr"
)

// HDR10Info is the static HDR10 metadata of a video stream. HEVC usually
// carries it only in SEI messages, so it shows up as frame side data, not
// in the stream's side_data_list.
type HDR10Info struct {
	HasMasteringDisplay bool
	// MasterDisplay is x265's master-display string
	// G(x,y)B(x,y)R(x,y)WP(x,y)L(max,min) in 0.00002 / 0.0001 cd/m² units.
	MasterDisplay string
	MaxLuminance  float64 // cd/m²
	MinLuminance  float64 // cd/m²
	HasCLL        bool
	MaxCLL        int
	MaxFALL       int
	// HDR10Plus is true when SMPTE 2094-40 dynamic metadata is present;
	// the encoders here do not carry it over.
	HDR10Plus bool
	// Problem describes mastering display values that cannot be right
	// (e.g. degenerate primaries). Empty when the data looks sane.
	Problem string
}

type sideData struct {
	Type         string `json:"side_data_type"`
	RedX         string `json:"red_x"`
	RedY         string `json:"red_y"`
	GreenX       string `json:"green_x"`
	GreenY       string `json:"green_y"`
	BlueX        string `json:"blue_x"`
	BlueY        string `json:"blue_y"`
	WhitePointX  string `json:"white_point_x"`
	WhitePointY  string `json:"white_point_y"`
	MinLuminance string `json:"min_luminance"`
	MaxLuminance string `json:"max_luminance"`
	MaxContent   *int   `json:"max_content"`
	MaxAverage   *int   `json:"max_average"`
}

// ProbeHDR10 reads HDR10 metadata from the first frame of the first video
// stream. It decodes one frame, so callers should only use it for HDR sources.
func ProbeHDR10(ffprobePath, input string) (HDR10Info, error) {
	cmd := exec.Command(ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-select_streams", "v:0",
		"-read_intervals", "%+#1",
		"-show_frames",
		"-show_entries", "frame=side_data_list:stream=side_data_list",
		"-show_streams",
		input,
	)
	procattr.HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return HDR10Info{}, fmt.Errorf("ffprobe hdr10: %w", err)
	}
	return parseHDR10JSON(out)
}

func parseHDR10JSON(out []byte) (HDR10Info, error) {
	var res struct {
		Frames []struct {
			SideData []sideData `json:"side_data_list"`
		} `json:"frames"`
		Streams []struct {
			SideData []sideData `json:"side_data_list"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return HDR10Info{}, fmt.Errorf("parse ffprobe hdr10 json: %w", err)
	}
	// Stream-level data (MKV Colour elements) first; frame SEI overrides it.
	var all []sideData
	for _, s := range res.Streams {
		all = append(all, s.SideData...)
	}
	if len(res.Frames) > 0 {
		all = append(all, res.Frames[0].SideData...)
	}
	var info HDR10Info
	for _, sd := range all {
		t := strings.ToLower(sd.Type)
		switch {
		case strings.Contains(t, "mastering display"):
			applyMasteringDisplay(&info, sd)
		case strings.Contains(t, "content light level"):
			if sd.MaxContent != nil && sd.MaxAverage != nil {
				info.HasCLL = true
				info.MaxCLL = *sd.MaxContent
				info.MaxFALL = *sd.MaxAverage
			}
		case strings.Contains(t, "2094-40") || strings.Contains(t, "hdr10+"):
			info.HDR10Plus = true
		}
	}
	return info, nil
}

func applyMasteringDisplay(info *HDR10Info, sd sideData) {
	vals := make([]float64, 0, 10)
	for _, raw := range []string{sd.GreenX, sd.GreenY, sd.BlueX, sd.BlueY, sd.RedX, sd.RedY,
		sd.WhitePointX, sd.WhitePointY, sd.MaxLuminance, sd.MinLuminance} {
		v, ok := parseRational(raw)
		if !ok {
			return
		}
		vals = append(vals, v)
	}
	gx, gy, bx, by, rx, ry, wx, wy, maxL, minL := vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7], vals[8], vals[9]
	info.HasMasteringDisplay = true
	info.MaxLuminance = maxL
	info.MinLuminance = minL
	info.MasterDisplay = ""
	info.Problem = masteringProblem(gx, gy, bx, by, rx, ry, wx, wy, maxL, minL)
	if info.Problem != "" {
		return
	}
	c := func(v float64) int64 { return int64(math.Round(v * 50000)) }
	l := func(v float64) int64 { return int64(math.Round(v * 10000)) }
	info.MasterDisplay = fmt.Sprintf("G(%d,%d)B(%d,%d)R(%d,%d)WP(%d,%d)L(%d,%d)",
		c(gx), c(gy), c(bx), c(by), c(rx), c(ry), c(wx), c(wy), l(maxL), l(minL))
}

// masteringProblem rejects values no real display has. BT.709, P3 and
// BT.2020 gamuts all span a CIE xy triangle area of at least ~0.11.
func masteringProblem(gx, gy, bx, by, rx, ry, wx, wy, maxL, minL float64) string {
	for _, v := range []float64{gx, gy, bx, by, rx, ry, wx, wy} {
		if v <= 0 || v >= 1 {
			return "współrzędne barw poza zakresem"
		}
	}
	area := math.Abs((gx-rx)*(by-ry)-(bx-rx)*(gy-ry)) / 2
	if area < 0.05 {
		return "zdegenerowane barwy podstawowe (np. x = y)"
	}
	if maxL <= 0 || maxL > 10000 || minL < 0 || minL >= maxL {
		return "nieprawidłowa luminancja"
	}
	return ""
}

func parseRational(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	num, den, found := strings.Cut(raw, "/")
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	if !found {
		return n, true
	}
	d, err := strconv.ParseFloat(den, 64)
	if err != nil || d == 0 {
		return 0, false
	}
	return n / d, true
}

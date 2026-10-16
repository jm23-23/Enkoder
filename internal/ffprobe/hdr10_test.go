package ffprobe

import (
	"os"
	"os/exec"
	"testing"
)

const p3D65Frame = `{"frames":[{"side_data_list":[
 {"side_data_type":"Mastering display metadata",
  "red_x":"34000/50000","red_y":"16000/50000","green_x":"13250/50000","green_y":"34500/50000",
  "blue_x":"7500/50000","blue_y":"3000/50000","white_point_x":"15635/50000","white_point_y":"16450/50000",
  "min_luminance":"50/10000","max_luminance":"10000000/10000"},
 {"side_data_type":"Content light level metadata","max_content":1000,"max_average":400}
]}],"streams":[{}]}`

func TestParseHDR10P3D65(t *testing.T) {
	info, err := parseHDR10JSON([]byte(p3D65Frame))
	if err != nil {
		t.Fatal(err)
	}
	want := "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"
	if info.MasterDisplay != want || info.Problem != "" {
		t.Fatalf("got %q (problem %q), want %q", info.MasterDisplay, info.Problem, want)
	}
	if info.MaxLuminance != 1000 || !info.HasCLL || info.MaxCLL != 1000 || info.MaxFALL != 400 {
		t.Fatalf("unexpected luminance/CLL: %+v", info)
	}
	if info.HDR10Plus {
		t.Fatal("no HDR10+ expected")
	}
}

// Real-world broken SEI: every primary has x == y (seen on a WEB-DL release).
func TestParseHDR10DegeneratePrimaries(t *testing.T) {
	broken := `{"frames":[{"side_data_list":[
 {"side_data_type":"Mastering display metadata",
  "red_x":"34000/50000","red_y":"34000/50000","green_x":"13250/50000","green_y":"13250/50000",
  "blue_x":"7500/50000","blue_y":"7500/50000","white_point_x":"15635/50000","white_point_y":"15635/50000",
  "min_luminance":"1/10000","max_luminance":"10000000/10000"},
 {"side_data_type":"HDR Dynamic Metadata SMPTE2094-40 (HDR10+)","num_windows":1}
]}]}`
	info, err := parseHDR10JSON([]byte(broken))
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasMasteringDisplay || info.Problem == "" || info.MasterDisplay != "" {
		t.Fatalf("degenerate primaries must be flagged and not exported: %+v", info)
	}
	if !info.HDR10Plus {
		t.Fatal("expected HDR10+ detection")
	}
}

func TestParseHDR10None(t *testing.T) {
	info, err := parseHDR10JSON([]byte(`{"frames":[{}],"streams":[{}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.HasMasteringDisplay || info.HasCLL || info.HDR10Plus {
		t.Fatalf("expected empty info, got %+v", info)
	}
}

func TestProbeHDR10Live(t *testing.T) {
	path := os.Getenv("ENKODER_HDR_SAMPLE")
	if path == "" {
		t.Skip("set ENKODER_HDR_SAMPLE to an HDR10 file")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not available")
	}
	info, err := ProbeHDR10(ffprobe, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", info)
	if !info.HasMasteringDisplay {
		t.Fatal("expected mastering display metadata")
	}
}

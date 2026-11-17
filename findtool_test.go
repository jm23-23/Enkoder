//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFFmpeg writes a shell script that answers -version and -filters /
// -encoders like ffmpeg, into dir, and returns its path.
func fakeTool(t *testing.T, dir, name, filters string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	script := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    -version) echo "` + name + ` version 9.9-test Copyright (c) the FFmpeg developers"; exit 0 ;;
    -filters) printf '%s\n' "` + filters + `"; exit 0 ;;
    -encoders) echo " V..... libsvtav1 SVT-AV1"; exit 0 ;;
  esac
done
exit 1
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCustomToolsFileAndFolder(t *testing.T) {
	dir := t.TempDir()
	ff := fakeTool(t, dir, "ffmpeg", "")
	fp := fakeTool(t, dir, "ffprobe", "")

	for _, in := range []string{ff, dir, " " + dir + " ", `"` + ff + `"`} {
		gotFF, gotFP, err := customTools(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if gotFF != ff || gotFP != fp {
			t.Errorf("%q: got %q, %q", in, gotFF, gotFP)
		}
	}
}

func TestCustomToolsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "opt", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ff := fakeTool(t, bin, "ffmpeg", "")
	got, probe, err := customTools("~/opt/bin")
	if err != nil || got != ff {
		t.Fatalf("got %q, %v", got, err)
	}
	if probe != "" {
		t.Errorf("no ffprobe in the folder, got %q", probe)
	}
}

func TestCustomToolsErrors(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "ffmpeg.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"", "relative/ffmpeg", filepath.Join(dir, "missing"), dir, plain} {
		if _, _, err := customTools(in); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func TestResolveToolsOrder(t *testing.T) {
	setDir, envDir := t.TempDir(), t.TempDir()
	setFF := fakeTool(t, setDir, "ffmpeg", "")
	setFP := fakeTool(t, setDir, "ffprobe", "")
	envFF := fakeTool(t, envDir, "ffmpeg", "")
	t.Setenv("ENKODER_FFMPEG", envFF)
	t.Setenv("ENKODER_FFPROBE", "")

	c := resolveTools(setDir)
	if c.Source != toolSourceSetting || c.FFmpeg != setFF || c.FFprobe != setFP || c.Problem != "" {
		t.Errorf("setting: %+v", c)
	}

	c = resolveTools("")
	if c.Source != toolSourceEnv || c.FFmpeg != envFF {
		t.Errorf("env: %+v", c)
	}

	// A stale setting falls back to the env variable and says why.
	c = resolveTools(filepath.Join(setDir, "gone"))
	if c.Source != toolSourceEnv || c.FFmpeg != envFF || !strings.Contains(c.Problem, "gone") {
		t.Errorf("fallback: %+v", c)
	}

	t.Setenv("ENKODER_FFMPEG", "")
	c = resolveTools("")
	if c.Source != toolSourceAuto || c.Problem != "" {
		t.Errorf("auto: %+v", c)
	}
}

func TestInspectFFmpeg(t *testing.T) {
	dir := t.TempDir()
	fakeTool(t, dir, "ffmpeg", " ... libvmaf           VV->V      Calculate the VMAF")
	fakeTool(t, dir, "ffprobe", "")
	d := inspectFFmpeg(dir)
	if !d.OK || d.Version != "9.9-test" || d.Metric != "vmaf" || !d.SVTAV1 || d.NVENC {
		t.Errorf("got %+v", d)
	}

	broken := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if d := inspectFFmpeg(broken); d.OK || d.Error == "" {
		t.Errorf("broken ffmpeg accepted: %+v", d)
	}
}

func TestUpdateSettingsKeepsOtherFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveSettings(settings{Lang: langPL}); err != nil {
		t.Fatal(err)
	}
	if err := updateSettings(func(s *settings) { s.FFmpegPath = "/opt/ffmpeg/bin/ffmpeg" }); err != nil {
		t.Fatal(err)
	}
	if err := updateSettings(func(s *settings) { s.Lang = langEN }); err != nil {
		t.Fatal(err)
	}
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Lang != langEN || s.FFmpegPath != "/opt/ffmpeg/bin/ffmpeg" {
		t.Errorf("got %+v", s)
	}
}

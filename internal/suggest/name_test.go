package suggest

import (
	"path/filepath"
	"testing"
)

func TestOutputNameCodecTags(t *testing.T) {
	in := filepath.Join("dir", "Movie.1992.1080p.BluRay.x264-GRP.mkv")
	cases := []struct {
		tag, container string
		scale          int
		want           string
	}{
		{TagHEVC, "mkv", 0, "Movie.1992.1080p.BluRay.x265-GRP.mkv"},
		{TagAV1, "mkv", 0, "Movie.1992.1080p.BluRay.AV1-GRP.mkv"},
		{TagAV1, "mp4", 720, "Movie.1992.720p.BluRay.AV1-GRP.mp4"},
		{TagNone, "mkv", 0, "Movie.1992.1080p.BluRay.x264-GRP.mkv"},
	}
	for _, c := range cases {
		got := OutputName(in, c.container, c.tag, c.scale)
		if want := filepath.Join("dir", c.want); got != want {
			t.Errorf("OutputName(%q, %q, %d) = %q, want %q", c.tag, c.container, c.scale, got, want)
		}
	}
	if got := OutputName("Clip.mkv", "mkv", TagAV1, 0); got != "Clip.AV1.mkv" {
		t.Errorf("untagged source: got %q", got)
	}
	if got := OutputName("Show.HEVC.mkv", "mkv", TagHEVC, 0); got != "Show.HEVC.mkv" {
		t.Errorf("HEVC source must keep its tag: got %q", got)
	}
}

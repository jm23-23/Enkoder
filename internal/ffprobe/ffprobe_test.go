package ffprobe

import (
	"encoding/json"
	"testing"
)

func TestBitsAndHDR(t *testing.T) {
	s := Stream{PixFmt: "yuv420p10le", ColorTransfer: "smpte2084"}
	if s.BitsPerSample() != 10 {
		t.Fatalf("bits=%d", s.BitsPerSample())
	}
	if !s.IsHDR() {
		t.Fatal("expected HDR from PQ transfer")
	}
	s2 := Stream{
		PixFmt: "yuv420p",
		SideDataList: []json.RawMessage{
			json.RawMessage(`{"side_data_type":"Mastering display metadata"}`),
		},
	}
	if !s2.IsHDR() {
		t.Fatal("expected HDR from side data")
	}
	if (Stream{FieldOrder: "tt"}).IsInterlaced() != true {
		t.Fatal("interlaced")
	}
	if (Stream{FieldOrder: "progressive"}).IsInterlaced() {
		t.Fatal("progressive")
	}
}

func TestDurationSeconds(t *testing.T) {
	res := &ProbeResult{
		Format: Format{Duration: ""},
		Streams: []Stream{
			{CodecType: "video", Duration: "12.5"},
			{CodecType: "audio", Tags: map[string]string{"DURATION": "00:00:15.25"}},
		},
	}
	got := DurationSeconds(res)
	if got < 15.2 || got > 15.3 {
		t.Fatalf("got %v want ~15.25 (tag wins over shorter stream)", got)
	}
	res.Format.Duration = "100.0"
	if got := DurationSeconds(res); got != 100 {
		t.Fatalf("format should win when longer, got %v", got)
	}
}

package main

import "testing"

func TestParseNVIDIASMIOutput(t *testing.T) {
	var s systemStatsDTO
	parseNVIDIASMIOutput("12, 48, 1024, 6144\n", &s)
	if s.GPUPercent == nil || *s.GPUPercent != 12 {
		t.Fatalf("gpu=%v", s.GPUPercent)
	}
	if s.GPUTempC == nil || *s.GPUTempC != 48 {
		t.Fatalf("temp=%v", s.GPUTempC)
	}
	if s.VRAMUsedMB == nil || *s.VRAMUsedMB != 1024 {
		t.Fatalf("vram used=%v", s.VRAMUsedMB)
	}
	if s.VRAMTotalMB == nil || *s.VRAMTotalMB != 6144 {
		t.Fatalf("vram total=%v", s.VRAMTotalMB)
	}

	var empty systemStatsDTO
	parseNVIDIASMIOutput("bad", &empty)
	if empty.GPUPercent != nil {
		t.Fatal("malformed should leave fields nil")
	}
}

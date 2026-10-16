//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseProcStatCPULine(t *testing.T) {
	idle, total, ok := parseProcStatCPULine("cpu  10 20 30 40 50 0 0 0 0 0")
	if !ok {
		t.Fatal("expected ok")
	}
	if idle != 90 {
		t.Fatalf("idle=%d want 90", idle)
	}
	if total != 150 {
		t.Fatalf("total=%d want 150", total)
	}
	_, _, ok = parseProcStatCPULine("cpu0 1 2 3 4")
	if ok {
		t.Fatal("cpu0 line should not parse as aggregate")
	}
}

func TestParseMeminfoKB(t *testing.T) {
	v, ok := parseMeminfoKB("MemTotal:       32029368 kB")
	if !ok || v != 32029368 {
		t.Fatalf("got %d ok=%v", v, ok)
	}
}

func TestCPUPercentFromIdleTotal(t *testing.T) {
	cpuSampleMu.Lock()
	cpuHaveSample = false
	cpuSampleMu.Unlock()

	if p := cpuPercentFromIdleTotal(90, 150); p != nil {
		t.Fatal("first sample should be nil")
	}
	p := cpuPercentFromIdleTotal(100, 200)
	if p == nil {
		t.Fatal("expected percent")
	}
	if *p < 79.9 || *p > 80.1 {
		t.Fatalf("got %v want ~80", *p)
	}
}

func TestCollectSystemStatsSmoke(t *testing.T) {
	_ = collectSystemStats()
	time.Sleep(50 * time.Millisecond)
	s := collectSystemStats()
	if s.MemTotalMB == nil || *s.MemTotalMB == 0 {
		t.Fatal("expected MemTotal from /proc/meminfo")
	}
	if s.MemUsedMB == nil {
		t.Fatal("expected MemUsed")
	}
	if *s.MemUsedMB > *s.MemTotalMB {
		t.Fatalf("used %d > total %d", *s.MemUsedMB, *s.MemTotalMB)
	}
}

func TestReadFirstTempInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "temp1_input")
	if err := os.WriteFile(path, []byte("54000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, ok := readFirstTempInput(dir)
	if !ok || v < 53.9 || v > 54.1 {
		t.Fatalf("got %v ok=%v", v, ok)
	}
}

func TestReadNVIDIASMI(t *testing.T) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		t.Skip("nvidia-smi not installed")
	}
	var s systemStatsDTO
	readNVIDIASMI(&s)
	if s.VRAMTotalMB == nil || *s.VRAMTotalMB == 0 {
		t.Fatal("expected VRAM total from nvidia-smi")
	}
	if s.GPUPercent == nil {
		t.Fatal("expected GPU utilization")
	}
}

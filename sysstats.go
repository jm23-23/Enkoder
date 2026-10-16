package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// systemStatsDTO is pushed to the UI about once per second.
// Missing sensors use JSON null → the UI shows a dash.
type systemStatsDTO struct {
	CPUPercent  *float64 `json:"cpuPercent"`
	GPUPercent  *float64 `json:"gpuPercent"`
	CPUTempC    *float64 `json:"cpuTempC"`
	GPUTempC    *float64 `json:"gpuTempC"`
	MemUsedMB   *uint64  `json:"memUsedMB"`
	MemTotalMB  *uint64  `json:"memTotalMB"`
	VRAMUsedMB  *uint64  `json:"vramUsedMB"`
	VRAMTotalMB *uint64  `json:"vramTotalMB"`
}

var (
	cpuSampleMu   sync.Mutex
	cpuPrevIdle   uint64
	cpuPrevTotal  uint64
	cpuHaveSample bool
)

func (a *app) systemStats() (systemStatsDTO, error) {
	return collectSystemStats(), nil
}

// startStatsPump pushes stats via Eval ~1 Hz. On Linux, Bind return values
// still use stock Eval (no-op), so the UI must receive updates this way.
func (a *app) startStatsPump() {
	go func() {
		time.Sleep(350 * time.Millisecond)
		_ = collectSystemStats() // seed CPU counters
		time.Sleep(200 * time.Millisecond)
		push := func() {
			payload, err := json.Marshal(collectSystemStats())
			if err != nil {
				return
			}
			js := fmt.Sprintf(`(function(){ if (typeof applySystemStats === "function") applySystemStats(%s); })()`, payload)
			a.view.Dispatch(func() {
				a.view.Eval(js)
			})
		}
		push()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			push()
		}
	}()
}

func ptrFloat(v float64) *float64 { return &v }
func ptrUint64(v uint64) *uint64  { return &v }

func cpuPercentFromIdleTotal(idle, total uint64) *float64 {
	cpuSampleMu.Lock()
	defer cpuSampleMu.Unlock()
	if !cpuHaveSample || total <= cpuPrevTotal {
		cpuPrevIdle, cpuPrevTotal = idle, total
		cpuHaveSample = true
		return nil
	}
	dTotal := total - cpuPrevTotal
	dIdle := idle - cpuPrevIdle
	cpuPrevIdle, cpuPrevTotal = idle, total
	if dTotal == 0 {
		return nil
	}
	busy := 100 * float64(dTotal-dIdle) / float64(dTotal)
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return ptrFloat(busy)
}

// readNVIDIASMI fills GPU util/temp/VRAM when nvidia-smi is available.
func readNVIDIASMI(dst *systemStatsDTO) {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=utilization.gpu,temperature.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return
	}
	parseNVIDIASMIOutput(string(out), dst)
}

func parseNVIDIASMIOutput(out string, dst *systemStatsDTO) {
	line := strings.TrimSpace(strings.Split(out, "\n")[0])
	if line == "" {
		return
	}
	parts := strings.Split(line, ",")
	if len(parts) < 4 {
		return
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if v, err := strconv.ParseFloat(parts[0], 64); err == nil {
		dst.GPUPercent = ptrFloat(v)
	}
	if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
		dst.GPUTempC = ptrFloat(v)
	}
	if v, err := strconv.ParseUint(parts[2], 10, 64); err == nil {
		dst.VRAMUsedMB = ptrUint64(v)
	}
	if v, err := strconv.ParseUint(parts[3], 10, 64); err == nil {
		dst.VRAMTotalMB = ptrUint64(v)
	}
}

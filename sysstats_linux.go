//go:build linux

package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func collectSystemStats() systemStatsDTO {
	var s systemStatsDTO
	if idle, total, ok := readProcStatCPU(); ok {
		s.CPUPercent = cpuPercentFromIdleTotal(idle, total)
	}
	if used, total, ok := readProcMeminfo(); ok {
		s.MemUsedMB = ptrUint64(used)
		s.MemTotalMB = ptrUint64(total)
	}
	if t, ok := readCPUTempC(); ok {
		s.CPUTempC = ptrFloat(t)
	}
	readNVIDIASMI(&s)
	return s
}

func readProcStatCPU() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	return parseProcStatCPULine(sc.Text())
}

func parseProcStatCPULine(line string) (idle, total uint64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var sum uint64
	vals := make([]uint64, 0, len(fields)-1)
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		vals = append(vals, v)
		sum += v
	}
	// idle = idle + iowait (fields 4 and 5, 0-based in vals)
	idle = vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}
	return idle, sum, true
}

func readProcMeminfo() (usedMB, totalMB uint64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	var totalKB, availKB uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			totalKB, haveTotal = parseMeminfoKB(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			availKB, haveAvail = parseMeminfoKB(line)
		}
		if haveTotal && haveAvail {
			break
		}
	}
	if !haveTotal || totalKB == 0 {
		return 0, 0, false
	}
	usedKB := totalKB
	if haveAvail && availKB <= totalKB {
		usedKB = totalKB - availKB
	}
	return usedKB / 1024, totalKB / 1024, true
}

func parseMeminfoKB(line string) (uint64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, false
	}
	v, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func readCPUTempC() (float64, bool) {
	if t, ok := readHwmonCPUTemp(); ok {
		return t, true
	}
	return readThermalZoneCPUTemp()
}

func readHwmonCPUTemp() (float64, bool) {
	entries, err := os.ReadDir("/sys/class/hwmon")
	if err != nil {
		return 0, false
	}
	prefer := []string{"coretemp", "k10temp", "zenpower", "cpu_thermal", "acpitz"}
	var fallback float64
	var haveFallback bool
	for _, pref := range prefer {
		for _, e := range entries {
			name, err := os.ReadFile(filepath.Join("/sys/class/hwmon", e.Name(), "name"))
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(name)) != pref {
				continue
			}
			if t, ok := readFirstTempInput(filepath.Join("/sys/class/hwmon", e.Name())); ok {
				if pref == "acpitz" {
					fallback, haveFallback = t, true
					continue
				}
				return t, true
			}
		}
	}
	if haveFallback {
		return fallback, true
	}
	return 0, false
}

func readFirstTempInput(dir string) (float64, bool) {
	// Prefer package/temp1, else first readable temp*_input.
	candidates := []string{"temp1_input"}
	matches, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
	candidates = append(candidates, matches...)
	seen := map[string]bool{}
	for _, path := range candidates {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || v <= 0 {
			continue
		}
		// hwmon temps are millidegrees Celsius
		return float64(v) / 1000, true
	}
	return 0, false
}

func readThermalZoneCPUTemp() (float64, bool) {
	zones, err := filepath.Glob("/sys/class/thermal/thermal_zone*/type")
	if err != nil {
		return 0, false
	}
	prefer := []string{"x86_pkg_temp", "TCPU", "cpu-thermal", "CPU", "soc_thermal"}
	var fallback float64
	var haveFallback bool
	for _, pref := range prefer {
		for _, typePath := range zones {
			b, err := os.ReadFile(typePath)
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(b)) != pref {
				continue
			}
			tempPath := filepath.Join(filepath.Dir(typePath), "temp")
			if t, ok := readMillidegC(tempPath); ok {
				return t, true
			}
		}
	}
	// Any zone as last resort (skip wifi etc.)
	for _, typePath := range zones {
		b, err := os.ReadFile(typePath)
		if err != nil {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(string(b)))
		if strings.Contains(typ, "wifi") || strings.Contains(typ, "nvme") || strings.Contains(typ, "pch") {
			continue
		}
		if strings.Contains(typ, "cpu") || strings.Contains(typ, "pkg") || typ == "acpitz" {
			tempPath := filepath.Join(filepath.Dir(typePath), "temp")
			if t, ok := readMillidegC(tempPath); ok {
				if typ == "acpitz" {
					fallback, haveFallback = t, true
					continue
				}
				return t, true
			}
		}
	}
	if haveFallback {
		return fallback, true
	}
	return 0, false
}

func readMillidegC(path string) (float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return float64(v) / 1000, true
}

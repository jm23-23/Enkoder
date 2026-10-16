//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = modKernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = modKernel32.NewProc("GlobalMemoryStatusEx")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func collectSystemStats() systemStatsDTO {
	var s systemStatsDTO
	if idle, total, ok := readSystemTimesCPU(); ok {
		s.CPUPercent = cpuPercentFromIdleTotal(idle, total)
	}
	if used, total, ok := readGlobalMemory(); ok {
		s.MemUsedMB = ptrUint64(used)
		s.MemTotalMB = ptrUint64(total)
	}
	// CPU package temp needs WMI/PDH; skip rather than shelling out.
	readNVIDIASMI(&s)
	return s
}

func readSystemTimesCPU() (idle, total uint64, ok bool) {
	var idleFT, kernelFT, userFT windows.Filetime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleFT)),
		uintptr(unsafe.Pointer(&kernelFT)),
		uintptr(unsafe.Pointer(&userFT)),
	)
	if r == 0 {
		return 0, 0, false
	}
	idle = filetimeToUint64(idleFT)
	kernel := filetimeToUint64(kernelFT)
	user := filetimeToUint64(userFT)
	// Kernel time includes idle on Windows.
	if kernel < idle {
		return 0, 0, false
	}
	total = kernel + user
	return idle, total, true
}

func filetimeToUint64(ft windows.Filetime) uint64 {
	return (uint64(ft.HighDateTime) << 32) | uint64(ft.LowDateTime)
}

func readGlobalMemory() (usedMB, totalMB uint64, ok bool) {
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st)))
	if r == 0 || st.TotalPhys == 0 {
		return 0, 0, false
	}
	used := st.TotalPhys - st.AvailPhys
	return used / (1024 * 1024), st.TotalPhys / (1024 * 1024), true
}

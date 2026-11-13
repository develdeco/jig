//go:build windows

package machine

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx is the MEMORYSTATUSEX structure of GlobalMemoryStatusEx.
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

func totalMemory() uint64 {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	st := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, _ := proc.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return 0
	}
	return st.TotalPhys
}

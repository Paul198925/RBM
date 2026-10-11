package sysinfo

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx is MEMORYSTATUSEX of the Windows API.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

var globalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// ReadMemory returns the memory figures of the machine.
func ReadMemory() (Memory, error) {
	var status memoryStatusEx
	status.length = uint32(unsafe.Sizeof(status))
	ok, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return Memory{}, err
	}
	// what Windows calls "page file" here is the commit limit and what is
	// left of it for this process
	return Memory{
		TotalMiB:       float64(status.totalPhys) / mib,
		FreeMiB:        float64(status.availPhys) / mib,
		CommitLimitMiB: float64(status.totalPageFile) / mib,
		CommitFreeMiB:  float64(status.availPageFile) / mib,
	}, nil
}

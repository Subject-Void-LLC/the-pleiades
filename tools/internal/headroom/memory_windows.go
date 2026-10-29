//go:build windows

// Windows' memory: GlobalMemoryStatusEx, which golang.org/x/sys/windows
// does not wrap, called through kernel32 directly.
package headroom

import (
	"fmt"
	"syscall"
	"unsafe"
)

// memoryStatusEx is MEMORYSTATUSEX, as kernel32 fills it.
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

// globalMemoryStatusEx is kernel32's GlobalMemoryStatusEx.
var globalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// read is Read on Windows. The swap in use is what is committed beyond the
// physical memory in use: an estimate of the page file's share, which is
// enough for growth to be seen.
func read() (Memory, error) {
	var s memoryStatusEx
	s.length = uint32(unsafe.Sizeof(s))
	ok, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&s))) // #nosec G103 -- the documented way to call it
	if ok == 0 {
		return Memory{}, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	const mb = 1024 * 1024
	physUsed := float64(s.totalPhys-s.availPhys) / mb
	committed := float64(s.totalPageFile-s.availPageFile) / mb
	return Memory{
		TotalMB:     float64(s.totalPhys) / mb,
		AvailableMB: float64(s.availPhys) / mb,
		SwapUsedMB:  max(0, committed-physUsed),
	}, nil
}

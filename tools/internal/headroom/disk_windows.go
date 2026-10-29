//go:build windows

// The space free on a volume, as GetDiskFreeSpaceEx reports it to this
// user.
package headroom

import (
	"golang.org/x/sys/windows"
)

// DiskFreeGB is the space free to this user on the volume holding path, in
// gigabytes (1 GB is 1024^3 bytes).
func DiskFreeGB(path string) (float64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(name, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return float64(free) / (1024 * 1024 * 1024), nil
}

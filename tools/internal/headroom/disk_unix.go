//go:build unix

// The space free on a file system, as statfs reports it to this user.
package headroom

import (
	"golang.org/x/sys/unix"
)

// DiskFreeGB is the space free to this user on the file system holding
// path, in gigabytes (1 GB is 1024^3 bytes), as df reports it: the blocks
// available to an unprivileged process, not the root-reserved ones.
func DiskFreeGB(path string) (float64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return float64(st.Bavail) * float64(st.Bsize) / (1024 * 1024 * 1024), nil // #nosec G115 -- block counts and sizes fit a float64
}

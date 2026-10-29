//go:build linux

// Linux's memory: /proc/meminfo, and the cgroup v2 limit this process runs
// under when it has one, since MemAvailable is the machine's where the
// limit is what the run will actually be killed at.
package headroom

import (
	"os"
	"path/filepath"
)

// read is Read on Linux.
func read() (Memory, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	m, err := parseMeminfo(string(data))
	if err != nil {
		return Memory{}, err
	}
	if limit, used, ok := cgroupMemory("/proc/self/cgroup", "/sys/fs/cgroup"); ok {
		m.LimitMB = limit
		m.AvailableMB = min(m.AvailableMB, max(0, limit-used))
	}
	return m, nil
}

// cgroupMemory is the memory limit and current use, in MB, of the cgroup v2
// named in cgroupFile under root, or false when the process is in the root
// cgroup, the cgroup has no limit, or either file cannot be read.
func cgroupMemory(cgroupFile, root string) (limitMB, usedMB float64, ok bool) {
	data, err := os.ReadFile(cgroupFile) // #nosec G304 -- a fixed system file, or a test's copy of it
	if err != nil {
		return 0, 0, false
	}
	path := parseCgroupPath(string(data))
	if path == "" {
		return 0, 0, false
	}
	dir := filepath.Join(root, filepath.FromSlash(path))
	maxText, err := os.ReadFile(filepath.Join(dir, "memory.max")) // #nosec G304 -- under the cgroup file system
	if err != nil {
		return 0, 0, false
	}
	limitMB, ok = parseCgroupBytes(string(maxText))
	if !ok {
		return 0, 0, false
	}
	current, err := os.ReadFile(filepath.Join(dir, "memory.current")) // #nosec G304 -- under the cgroup file system
	if err != nil {
		return 0, 0, false
	}
	usedMB, ok = parseCgroupBytes(string(current))
	if !ok {
		return 0, 0, false
	}
	return limitMB, usedMB, true
}

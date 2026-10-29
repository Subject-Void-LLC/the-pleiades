// Reading the machine's memory on each system this repository's tests run
// on, through what that system itself reports: /proc/meminfo on Linux (and
// the cgroup's own limit), vm_stat and sysctl on macOS, sysctl and swapinfo
// on FreeBSD, GlobalMemoryStatusEx on Windows. Each system's reader is in
// its own file; the parsers here take the text, so a test reads a captured
// answer rather than the machine it runs on.
package headroom

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Read is the machine's memory now, as the system this process runs on
// reports it. An error means it could not be read, which a caller treats as
// no reading: the run goes on unbounded by memory, and says so.
func Read() (Memory, error) {
	return read()
}

// kb is a /proc/meminfo value in kB, as MB.
func kb(value string) (float64, error) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return n / 1024, nil
}

// parseMeminfo reads MemTotal, MemAvailable, SwapTotal and SwapFree from
// /proc/meminfo's text. A file without MemAvailable (a kernel before 3.14)
// is refused rather than guessed from MemFree, which leaves the page cache
// out and would stop a healthy run.
func parseMeminfo(text string) (Memory, error) {
	var m Memory
	found := map[string]bool{}
	fields := map[string]*float64{"MemTotal": &m.TotalMB, "MemAvailable": &m.AvailableMB}
	var swapTotal, swapFree float64
	fields["SwapTotal"], fields["SwapFree"] = &swapTotal, &swapFree
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		name, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		dst, want := fields[name]
		if !want {
			continue
		}
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			continue
		}
		v, err := kb(parts[0])
		if err != nil {
			return Memory{}, fmt.Errorf("/proc/meminfo %s: %w", name, err)
		}
		*dst, found[name] = v, true
	}
	for _, name := range []string{"MemTotal", "MemAvailable"} {
		if !found[name] {
			return Memory{}, fmt.Errorf("/proc/meminfo has no %s line", name)
		}
	}
	m.SwapUsedMB = max(0, swapTotal-swapFree)
	return m, nil
}

// parseCgroupPath reads this process's cgroup v2 path from /proc/self/cgroup
// ("0::/user.slice/..."), or "" when the process is in the root cgroup or
// the file is not v2's.
func parseCgroupPath(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "0::"); ok {
			if path == "/" {
				return ""
			}
			return path
		}
	}
	return ""
}

// parseCgroupBytes reads a memory.max or memory.current value: a number of
// bytes as MB, or 0 and false for "max" (no limit) or anything else.
func parseCgroupBytes(text string) (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return 0, false
	}
	return n / (1024 * 1024), true
}

// parseVMStat reads macOS vm_stat's text: the page size from its first line
// and the free, inactive and speculative page counts, which together are
// what macOS can give a new allocation without swapping (the "available"
// a Linux kernel reports). The result is in MB.
func parseVMStat(text string) (float64, error) {
	pageSize := 0.0
	counts := map[string]float64{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if _, rest, ok := strings.Cut(line, "page size of "); ok {
			n, err := strconv.ParseFloat(strings.Fields(rest)[0], 64)
			if err != nil {
				return 0, fmt.Errorf("vm_stat page size: %w", err)
			}
			pageSize = n
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch name {
		case "Pages free", "Pages inactive", "Pages speculative":
			n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(rest), "."), 64)
			if err != nil {
				return 0, fmt.Errorf("vm_stat %s: %w", name, err)
			}
			counts[name] = n
		}
	}
	if pageSize == 0 {
		return 0, fmt.Errorf("vm_stat printed no page size")
	}
	if _, ok := counts["Pages free"]; !ok {
		return 0, fmt.Errorf("vm_stat printed no free page count")
	}
	pages := counts["Pages free"] + counts["Pages inactive"] + counts["Pages speculative"]
	return pages * pageSize / (1024 * 1024), nil
}

// parseSwapUsage reads the swap in use from macOS's vm.swapusage sysctl
// ("total = 2048.00M  used = 1024.50M  free = 1023.50M  (encrypted)"), in MB.
func parseSwapUsage(text string) (float64, error) {
	_, rest, ok := strings.Cut(text, "used = ")
	if !ok {
		return 0, fmt.Errorf("vm.swapusage says %q, with no used value", strings.TrimSpace(text))
	}
	return sizeMB(strings.Fields(rest)[0])
}

// sizeMB reads a size with a K, M or G suffix as macOS prints one, in MB.
func sizeMB(s string) (float64, error) {
	unit := 1.0
	switch {
	case strings.HasSuffix(s, "K"):
		unit = 1.0 / 1024
	case strings.HasSuffix(s, "M"):
		unit = 1
	case strings.HasSuffix(s, "G"):
		unit = 1024
	default:
		return 0, fmt.Errorf("%q has no K, M or G suffix", s)
	}
	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil {
		return 0, err
	}
	return n * unit, nil
}

// parseFreeBSD reads the four values one `sysctl -n hw.physmem hw.pagesize
// vm.stats.vm.v_free_count vm.stats.vm.v_inactive_count` prints, one per
// line: the physical memory in bytes, the page size, and the free and
// inactive page counts, which together are what FreeBSD can give a new
// allocation without paging. Total and available come back in MB.
func parseFreeBSD(text string) (totalMB, availableMB float64, err error) {
	lines := strings.Fields(text)
	if len(lines) != 4 {
		return 0, 0, fmt.Errorf("sysctl printed %d value(s), want 4", len(lines))
	}
	var v [4]float64
	for i, line := range lines {
		if v[i], err = strconv.ParseFloat(line, 64); err != nil {
			return 0, 0, fmt.Errorf("sysctl value %d: %w", i+1, err)
		}
	}
	return v[0] / (1024 * 1024), (v[2] + v[3]) * v[1] / (1024 * 1024), nil
}

// parseSwapinfo reads the swap in use from `swapinfo -k`'s text, in MB: the
// Used column of its last line, which is the one device or the Total of
// several; a header alone (no swap device) is 0.
func parseSwapinfo(text string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	last := strings.Fields(lines[len(lines)-1])
	if len(last) < 3 || last[0] == "Device" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(last[2], 64)
	if err != nil {
		return 0, fmt.Errorf("swapinfo used column: %w", err)
	}
	return n / 1024, nil
}

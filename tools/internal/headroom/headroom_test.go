// Tests for the policy's decisions, which are plain functions of readings,
// and for each system's parser, fed captured answers rather than the
// machine the test runs on.
package headroom

import (
	"strings"
	"testing"
)

// TestParallelism: the memory fits (available - reserve) / budget packages;
// what was asked for (or go test's default) is lowered to that and never
// under 1; an unknown reading leaves the ask alone.
func TestParallelism(t *testing.T) {
	for _, tc := range []struct {
		name       string
		available  float64
		want, cpus int
		ran, fit   int
	}{
		{"the WSL host, go test's default", 4300, 0, 20, 2, 2},
		{"the WSL host, asked for 1", 4300, 1, 20, 1, 2},
		{"a 4 GB VM with 4 CPUs", 3500, 0, 4, 2, 2},
		{"a short machine still runs one", 1500, 0, 4, 1, 0},
		{"a CI runner keeps go test's default", 14000, 0, 4, 4, 10},
		{"a big ask is lowered to the fit", 14000, 16, 4, 10, 10},
		{"an unknown reading leaves the ask", 0, 3, 4, 3, 0},
		{"an unknown reading and no ask is the CPU count", 0, 0, 6, 6, 0},
	} {
		ran, fit := Parallelism(tc.available, tc.want, tc.cpus)
		if ran != tc.ran || fit != tc.fit {
			t.Errorf("%s: Parallelism(%.0f, %d, %d) = %d, %d; want %d, %d", tc.name, tc.available, tc.want, tc.cpus, ran, fit, tc.ran, tc.fit)
		}
	}
}

// TestStop: a reading under the floor stops the run, and so does swap grown
// past the limit since the start; a reading with room, and no reading at
// all, do not.
func TestStop(t *testing.T) {
	start := Memory{TotalMB: 9942, AvailableMB: 4300, SwapUsedMB: 4600}
	for _, tc := range []struct {
		name string
		m    Memory
		want string
	}{
		{"room", Memory{TotalMB: 9942, AvailableMB: 2000, SwapUsedMB: 4700}, ""},
		{"under the floor", Memory{TotalMB: 9942, AvailableMB: 650, SwapUsedMB: 4700}, "memory available 650 MB, under the 700 MB floor"},
		{"swap grown", Memory{TotalMB: 9942, AvailableMB: 2000, SwapUsedMB: 4600 + SwapGrowthMB}, "swap in use grew 3072 MB since the run began, 3072 MB allowed"},
		{"swap grown but not yet past", Memory{TotalMB: 9942, AvailableMB: 2000, SwapUsedMB: 4600 + SwapGrowthMB - 1}, ""},
		{"no reading", Memory{}, ""},
	} {
		if got := Stop(tc.m, start); got != tc.want {
			t.Errorf("%s: Stop = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := Fall(Memory{AvailableMB: 3000, SwapUsedMB: 4800}, start); got != 1500 {
		t.Errorf("Fall = %.0f, want 1300 of memory and 200 of swap", got)
	}
}

// TestParseMeminfo reads a real /proc/meminfo shape, refuses one without
// MemAvailable, and reads swap in use as total minus free.
func TestParseMeminfo(t *testing.T) {
	text := "MemTotal:       10181280 kB\nMemFree:          115480 kB\nMemAvailable:    4314140 kB\nBuffers:          12 kB\nSwapTotal:       8388608 kB\nSwapFree:        3528652 kB\n"
	m, err := parseMeminfo(text)
	if err != nil {
		t.Fatal(err)
	}
	if int(m.TotalMB) != 9942 || int(m.AvailableMB) != 4213 || int(m.SwapUsedMB) != 4746 {
		t.Errorf("parseMeminfo = %+v", m)
	}
	if _, err := parseMeminfo("MemTotal: 1 kB\nMemFree: 1 kB\n"); err == nil || !strings.Contains(err.Error(), "no MemAvailable") {
		t.Errorf("a meminfo without MemAvailable: %v", err)
	}
	if _, err := parseMeminfo("MemTotal: lots kB\nMemAvailable: 1 kB\n"); err == nil {
		t.Error("a value that is not a number was accepted")
	}
}

// TestParseCgroup reads a cgroup v2 path and a memory.max value, and treats
// the root cgroup and "max" as no limit.
func TestParseCgroup(t *testing.T) {
	if got := parseCgroupPath("0::/user.slice/user-1000.slice/run-r1.scope\n"); got != "/user.slice/user-1000.slice/run-r1.scope" {
		t.Errorf("path = %q", got)
	}
	if got := parseCgroupPath("0::/\n"); got != "" {
		t.Errorf("the root cgroup gave %q", got)
	}
	if got := parseCgroupPath("12:memory:/x\n"); got != "" {
		t.Errorf("a v1 file gave %q", got)
	}
	if mb, ok := parseCgroupBytes("4718592000\n"); !ok || mb != 4500 {
		t.Errorf("4500 MiB = %.0f, %v", mb, ok)
	}
	if _, ok := parseCgroupBytes("max\n"); ok {
		t.Error("max read as a limit")
	}
}

// TestParseVMStat reads macOS's vm_stat: the page size from its header and
// the free, inactive and speculative pages as what is available.
func TestParseVMStat(t *testing.T) {
	text := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free:                               100000.\nPages active:                             500000.\nPages inactive:                           200000.\nPages speculative:                         20000.\nPages wired down:                         300000.\n"
	mb, err := parseVMStat(text)
	if err != nil {
		t.Fatal(err)
	}
	if mb != 5000 {
		t.Errorf("available = %.0f MB, want 5000 (320000 pages of 16 KiB)", mb)
	}
	if _, err := parseVMStat("Pages free: 1.\n"); err == nil {
		t.Error("vm_stat without a page size was accepted")
	}
	if used, err := parseSwapUsage("total = 2048.00M  used = 1024.50M  free = 1023.50M  (encrypted)\n"); err != nil || used != 1024.5 {
		t.Errorf("swap used = %.1f, %v", used, err)
	}
	if used, err := parseSwapUsage("total = 2.00G  used = 1.50G  free = 0.50G\n"); err != nil || used != 1536 {
		t.Errorf("swap used in G = %.1f, %v", used, err)
	}
	if _, err := parseSwapUsage("nothing"); err == nil {
		t.Error("a swapusage without used was accepted")
	}
}

// TestParseFreeBSD reads sysctl's four values and swapinfo's used column,
// with and without a swap device.
func TestParseFreeBSD(t *testing.T) {
	total, available, err := parseFreeBSD("4294967296\n4096\n100000\n50000\n")
	if err != nil || total != 4096 || int(available) != 585 {
		t.Errorf("parseFreeBSD = %.0f, %.0f, %v", total, available, err)
	}
	if _, _, err := parseFreeBSD("1\n2\n"); err == nil {
		t.Error("two values were accepted")
	}
	swap := "Device          1K-blocks     Used    Avail Capacity\n/dev/ada0p3       2097152    12288  2084864     1%\n"
	if used, err := parseSwapinfo(swap); err != nil || used != 12 {
		t.Errorf("swapinfo used = %.0f, %v", used, err)
	}
	if used, err := parseSwapinfo("Device          1K-blocks     Used    Avail Capacity\n"); err != nil || used != 0 {
		t.Errorf("no swap device: %.0f, %v", used, err)
	}
}

// TestRead reads this machine, whatever system it is: the total and what is
// available are positive, and what is available is at most the total.
func TestRead(t *testing.T) {
	m, err := Read()
	if err != nil {
		t.Skipf("no reading here: %v", err)
	}
	if m.TotalMB <= 0 || m.AvailableMB <= 0 || m.AvailableMB > m.TotalMB {
		t.Errorf("Read = %+v", m)
	}
}

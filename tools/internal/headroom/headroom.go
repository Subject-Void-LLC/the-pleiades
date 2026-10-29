// Package headroom is the one policy for what a test run may take of the
// machine it runs on, and the readings it is applied to: the memory free
// (and the swap in use), the disk free where the run builds and writes, how
// many packages that memory fits at once, and when a run must be stopped
// before the machine itself runs out.
//
// tools/testgate applies it wherever it runs, which is every place this
// repository's tests run: this machine, each OS check's machine (the Mac,
// the FreeBSD and Windows VMs) and a CI runner, since each of them runs
// that one command. So every environment is held to the same numbers
// rather than each to a guess of its own. Before it, the WSL host crashed
// four times from memory (a watchdog outside make caught only the local
// gate), a FreeBSD VM's kernel killed its compiles (its -p was its CPU
// count, on 4 GB), and a full disk read as 169 build failures with no
// compiler output. The numbers below are measured, not chosen, and each
// says where it came from.
package headroom

import (
	"fmt"
	"time"
)

// BudgetMB is the memory one package's tests may take at once, in MB:
// its test binary, its builds and the containers it starts, together. It is
// what sizes how many packages run at once (Parallelism). Measured on the
// 9.7 GiB WSL host on 2026-10-29, as each container package's fall in
// MemAvailable plus its swap growth while it ran alone: every one but
// tests/e2e's compose gates (6826 MB, left to CI) took 853 MB or less, with
// internal/inventory/onboard the largest at 1152 MB. 1200 holds the largest
// with a little room; a run's record says what each package really took
// (PackageMB), so the number can be moved from evidence.
const BudgetMB = 1200

// ReserveMB is what a run leaves the machine, in MB, whatever else it fits:
// the page cache a build thrashes without, and whatever shares the machine
// (an editor, a language server, the Docker daemon). One gigabyte is what
// this repository's memory caps have always been sized against (free
// memory minus about 1.5 GB for the cap itself, LESSONS 300).
const ReserveMB = 1024

// FloorMB is the MemAvailable under which a run is stopped, in MB. It is
// the monitor's floor (tools/monitor/memwatch.py): under it the kernel is
// about to reclaim what the run needs, and a stop now fails the gate where a
// minute later the machine fails.
const FloorMB = 700

// SwapGrowthMB is how much more swap may be in use than when the run began
// before it is stopped, in MB. MemAvailable alone missed the 2026-10-25
// crash: the kernel paged until all 8 GB of swap were gone and the load
// reached 600, while MemAvailable never went under the floor (FAILURE_PATTERNS
// 668's day; tools/monitor's SWAP_GROWTH_MB records it). Swap grows before
// that point, while there is still time to stop.
const SwapGrowthMB = 3072

// MinFreeGB is the space, in whole gigabytes, the file systems a run builds
// and writes on must have free before it starts. A run unpacks or builds
// every package it tests and their dependencies into Go's build cache:
// bsd-check's grew to 7.8 GB and filled its disk on 2026-10-19, and every
// package then failed to build with no compiler message reaching the report
// (FAILURE_PATTERNS 537); 5 GB of go-build folders left by killed runs did
// it again on 2026-10-27 (688).
const MinFreeGB = 4

// SampleEvery is how often the machine is read while a run goes: the
// monitor's interval, often enough to see a build's rise, seldom enough to
// cost nothing.
const SampleEvery = 5 * time.Second

// StopGrace is how long a stopped run gets to end after SIGTERM before
// SIGKILL: pr-check's own trap takes about this long to stop its legs, and a
// go test asked to stop tears its containers down first.
const StopGrace = 30 * time.Second

// Memory is one reading of the machine's memory, in MB.
type Memory struct {
	// TotalMB is the machine's physical memory.
	TotalMB float64
	// AvailableMB is what a new allocation can have without swapping: Linux's
	// MemAvailable, or the equivalent each other system reports (memory.go),
	// and, inside a cgroup with a memory limit, the smaller of that and what
	// the limit has left.
	AvailableMB float64
	// SwapUsedMB is the swap (or page file) in use.
	SwapUsedMB float64
	// LimitMB is a cgroup memory limit the process runs under, or 0 with
	// none: `capped` runs a gate under one on the WSL host.
	LimitMB float64
}

// Parallelism is how many packages a run may test at once on a machine with
// availableMB free: want, or cpus (go test's own default for -p) when want
// is 0, lowered to what the memory fits, (availableMB - ReserveMB) /
// BudgetMB, and never under 1, since one package must run for anything to
// be tested at all. fit is what the memory fits before the lowering, so a
// caller can say why; 0 when availableMB is unknown (0), which leaves want
// alone.
func Parallelism(availableMB float64, want, cpus int) (ran, fit int) {
	if want <= 0 {
		want = max(cpus, 1)
	}
	if availableMB <= 0 {
		return want, 0
	}
	fit = int((availableMB - ReserveMB) / BudgetMB)
	return max(1, min(want, fit)), fit
}

// Stop decides whether a reading means the run must stop now: the memory
// under FloorMB, or swap grown SwapGrowthMB past start's. It returns why, or
// "" to go on. A reading never taken (the zero Memory) stops nothing: no
// reading is not evidence of a shortage.
func Stop(m, start Memory) string {
	if m == (Memory{}) {
		return ""
	}
	if m.AvailableMB < FloorMB {
		return fmt.Sprintf("memory available %.0f MB, under the %d MB floor", m.AvailableMB, FloorMB)
	}
	if grown := m.SwapUsedMB - start.SwapUsedMB; grown >= SwapGrowthMB {
		return fmt.Sprintf("swap in use grew %.0f MB since the run began, %d MB allowed", grown, SwapGrowthMB)
	}
	return ""
}

// Fall is how far a reading has fallen below where the run began, in MB:
// the memory no longer available plus the swap taken since, which is what
// the run's work has cost the machine so far. Negative when the machine has
// more than it started with.
func Fall(m, start Memory) float64 {
	return (start.AvailableMB - m.AvailableMB) + (m.SwapUsedMB - start.SwapUsedMB)
}

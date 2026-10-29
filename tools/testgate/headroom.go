// The one resource policy, applied to this run (tools/internal/headroom):
// the disk check before anything builds, the parallelism the free memory
// allows, the sampler that follows the run and stops it before the machine
// runs out, the signal that stops it from outside, and the account of what
// the run took, printed and written to the record. It runs wherever
// testgate runs, so this machine, each OS check's machine and a CI runner
// are held to the same numbers.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/headroom"
)

// guard is the policy's hold on one run. It is the Observer each go test
// runs under (flakegate.RunGoTestJSONObserved).
type guard struct {
	// watch samples the machine while the run goes; nil when its memory
	// could not be read, which leaves the run unbounded by it.
	watch *headroom.Watch
	// disks samples the space free where the run builds and writes.
	disks *headroom.DiskWatch
	// divided says whether a package is one durations.json times test by
	// test, whose top-level tests are charged one by one too; nil charges
	// packages alone.
	divided func(pkg string) bool
	// start is the reading as the run began.
	start headroom.Memory
	// asked and ran are the -p the run was asked for and the one it uses.
	asked, ran, fit int
	// disk is the space free where the run builds and writes, by path.
	disk map[string]float64
	// signals is where an interrupt arrives.
	signals chan os.Signal

	mu sync.Mutex
	// current is the go test running now, or nil.
	current *exec.Cmd
	// kill is the delayed SIGKILL a stop armed, or nil; Ended disarms it.
	kill *time.Timer
	// stopped is why the run was stopped from here, or "".
	stopped string
}

// newGuard applies the policy before anything builds: it refuses a disk
// short of space, reads the machine's memory, lowers parallel to what that
// memory fits (saying so), and starts the samplers and the signal watch.
// parallel is the -p asked for (0: go test's default); packages are the
// run's, which resources.json's measurements pack into the memory
// (packedParallelism), or none for ./..., which the flat budget sizes
// (headroom.Parallelism); divided says which packages' tests are charged
// one by one (see guard.divided).
func newGuard(parallel int, packages []string, divided func(string) bool) (*guard, error) {
	g := &guard{asked: parallel, ran: parallel, divided: divided}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	paths := []string{cwd, os.TempDir()}
	g.disk, err = headroom.CheckDisk(paths...)
	if err != nil {
		return nil, err
	}
	g.disks = headroom.StartDiskWatch(paths, g.disk, headroom.DiskFreeGB, headroom.SampleEvery)
	m, err := headroom.Read()
	if err != nil {
		fmt.Printf("testgate: headroom: this machine's memory could not be read (%v), so the run is not bounded by it\n", err)
	} else {
		g.start = m
		g.ran, g.fit = headroom.Parallelism(m.AvailableMB, parallel, runtime.NumCPU())
		by := fmt.Sprintf("at %d MB a package", headroom.BudgetMB)
		if len(packages) > 0 {
			g.ran = packedParallelism(resources(), packages, m.AvailableMB, parallel, runtime.NumCPU(), headroom.BudgetMB, headroom.ReserveMB)
			g.fit = g.ran
			by = fmt.Sprintf("by the most memory %s records each needed (%d MB for one it has none for)", resourcesPath, headroom.BudgetMB)
		}
		if g.ran < wantOrDefault(parallel) {
			fmt.Printf("testgate: headroom: %d package(s) at once, not %s: %.0f MB free%s fits %d after the %d MB the machine keeps, %s\n",
				g.ran, askedText(parallel), m.AvailableMB, limitText(m), g.fit, headroom.ReserveMB, by)
		}
		g.watch = headroom.StartWatch(m, headroom.Read, headroom.SampleEvery, g.stop)
	}
	g.signals = make(chan os.Signal, 1)
	signal.Notify(g.signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		for sig := range g.signals {
			g.stop("interrupted by " + sig.String())
		}
	}()
	return g, nil
}

// wantOrDefault is the -p a run asked for as go test reads it: the CPU
// count for 0.
func wantOrDefault(parallel int) int {
	if parallel <= 0 {
		return max(runtime.NumCPU(), 1)
	}
	return parallel
}

// askedText names the -p a run asked for.
func askedText(parallel int) string {
	if parallel <= 0 {
		return fmt.Sprintf("go test's default of %d", runtime.NumCPU())
	}
	return fmt.Sprintf("the %d asked for", parallel)
}

// limitText names a cgroup limit a reading is under, or nothing.
func limitText(m headroom.Memory) string {
	if m.LimitMB > 0 {
		return fmt.Sprintf(" (under a %.0f MB memory limit)", m.LimitMB)
	}
	return ""
}

// Prepare bounds each go test's Go processes: GOMEMLIMIT at the package
// budget when the caller set none, so a test binary, a compile or a link
// collects harder as it nears the budget instead of growing through it. A
// GOMEMLIMIT the caller set (capped's, on the WSL host) stays.
func (g *guard) Prepare(cmd *exec.Cmd) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	cmd.Env = append(os.Environ(), fmt.Sprintf("GOMEMLIMIT=%dMiB", headroom.BudgetMB))
}

// Started keeps the running go test, which a stop signals.
func (g *guard) Started(cmd *exec.Cmd) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.current = cmd
}

// Ended forgets a go test that has been waited for, and disarms the delayed
// kill a stop may have armed against it, so neither reaches a process that
// is gone and a pid since reused.
func (g *guard) Ended(*exec.Cmd) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.current = nil
	if g.kill != nil {
		g.kill.Stop()
		g.kill = nil
	}
}

// Event charges the packages running at each reading: a package's start
// event begins its account, its own pass, fail or skip event ends it. A
// top-level test of a divided package (tests/e2e's) is charged one by one as
// well, under the key durations.json and resources.json use for it.
func (g *guard) Event(e flakegate.Event) {
	key := e.Package
	if e.Test != "" {
		if g.divided == nil || !g.divided(e.Package) || strings.Contains(e.Test, "/") {
			return
		}
		key = e.Package + " " + e.Test
	}
	switch e.Action {
	case "start", "run":
		g.began(key)
	case "pass", "fail", "skip":
		g.ended(key)
	}
}

// began and ended charge the watches that exist.
func (g *guard) began(key string) {
	if g.watch != nil {
		g.watch.Began(key)
	}
	if g.disks != nil {
		g.disks.Began(key)
	}
}

func (g *guard) ended(key string) {
	if g.watch != nil {
		g.watch.Ended(key)
	}
	if g.disks != nil {
		g.disks.Ended(key)
	}
}

// stop ends the run for reason, once: SIGTERM to the running go test's
// whole group, then SIGKILL after headroom.StopGrace to whatever is still
// there. The reason is kept for the verdict, so a run stopped here is never
// re-run in isolation or read as failures of its own.
func (g *guard) stop(reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped != "" {
		return
	}
	g.stopped = reason
	fmt.Fprintf(os.Stderr, "\ntestgate: stopping the run: %s\n", reason)
	cmd := g.current
	if cmd == nil {
		return
	}
	_ = flakegate.SignalGroup(cmd, syscall.SIGTERM)
	g.kill = time.AfterFunc(headroom.StopGrace, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		// Only while the same command is still the current one: Ended clears
		// it, and its pid may belong to something else by now.
		if g.current == cmd {
			_ = flakegate.SignalGroup(cmd, syscall.SIGKILL)
		}
	})
}

// Stopped is why the run was stopped from here, or "".
func (g *guard) Stopped() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopped
}

// peaks is what each package has cost in memory so far (headroom.Watch.Peaks),
// packages only, or nil with no watch; testPeaks the same for the tests
// charged one by one.
func (g *guard) peaks() map[string]float64 {
	if g.watch == nil {
		return nil
	}
	packages, _ := splitKeys(g.watch.Peaks())
	return packages
}

func (g *guard) testPeaks() map[string]float64 {
	if g.watch == nil {
		return nil
	}
	_, tests := splitKeys(g.watch.Peaks())
	return tests
}

// diskPeaks and testDiskPeaks are the same for what each wrote to disk.
func (g *guard) diskPeaks() map[string]float64 {
	if g.disks == nil {
		return nil
	}
	packages, _ := splitKeys(g.disks.Peaks())
	return packages
}

func (g *guard) testDiskPeaks() map[string]float64 {
	if g.disks == nil {
		return nil
	}
	_, tests := splitKeys(g.disks.Peaks())
	return tests
}

// splitKeys divides a watch's peaks into the packages' (an import path) and
// the tests' (an import path, a space and the test).
func splitKeys(peaks map[string]float64) (packages, tests map[string]float64) {
	packages, tests = map[string]float64{}, map[string]float64{}
	for key, v := range peaks {
		if strings.Contains(key, " ") {
			tests[key] = v
		} else {
			packages[key] = v
		}
	}
	return packages, tests
}

// finish ends the samplers and the signal watch and returns the run's
// account for its record. It may be called more than once.
func (g *guard) finish() *flakegate.Headroom {
	signal.Stop(g.signals)
	h := &flakegate.Headroom{AvailableMB: g.start.AvailableMB, LimitMB: g.start.LimitMB, ParallelAsked: g.asked, ParallelRan: g.ran, DiskFreeGB: g.disk, Stopped: g.Stopped()}
	if g.watch != nil {
		r := g.watch.Finish()
		h.MinAvailableMB, h.SwapGrowthMB, h.Samples = r.MinAvailableMB, r.SwapGrowthMB, r.Samples
		h.PackageMB, _ = splitKeys(r.PeakMB)
		if h.Stopped == "" {
			h.Stopped = r.Stopped
		}
	}
	if g.disks != nil {
		r := g.disks.Finish()
		h.PackageDiskMB, _ = splitKeys(r.PeakMB)
		h.MinDiskFreeGB = r.MinFreeGB
	}
	return h
}

// account is the one line a run ends with about what it took, for a reader
// of the log: the memory as it began and at its lowest, the swap it grew,
// how many packages ran at once, the package that cost the most, and the
// disk where it built.
func account(h *flakegate.Headroom) string {
	if h.Samples == 0 && h.AvailableMB == 0 {
		return fmt.Sprintf("testgate: headroom: memory not read; %s", diskText(h.DiskFreeGB))
	}
	parallel := fmt.Sprintf("%d package(s) at once", h.ParallelRan)
	if h.ParallelRan < wantOrDefault(h.ParallelAsked) {
		parallel += fmt.Sprintf(" (asked %s)", askedText(h.ParallelAsked))
	}
	largest := "no package measured"
	if pkg, mb := largestOf(h.PackageMB); pkg != "" {
		largest = fmt.Sprintf("the largest package cost %.0f MB (%s)", mb, strings.TrimPrefix(pkg, modulePrefix))
	}
	wrote := ""
	if pkg, mb := largestOf(h.PackageDiskMB); pkg != "" {
		wrote = fmt.Sprintf(" and the most disk written was %.0f MB (%s)", mb, strings.TrimPrefix(pkg, modulePrefix))
	}
	return fmt.Sprintf("testgate: headroom: %.0f MB free as the run began%s, %.0f MB at the least, swap grew %.0f MB; %s; %s%s; %s",
		h.AvailableMB, limitText(headroom.Memory{LimitMB: h.LimitMB}), h.MinAvailableMB, h.SwapGrowthMB, parallel, largest, wrote, diskText(h.DiskFreeGB))
}

// largestOf is the package with the largest cost, and it; "" with none.
func largestOf(peaks map[string]float64) (string, float64) {
	var pkg string
	var most float64
	for p, mb := range peaks {
		if pkg == "" || mb > most || (mb == most && p < pkg) {
			pkg, most = p, mb
		}
	}
	return pkg, most
}

// diskText names the space free under each path, sorted.
func diskText(free map[string]float64) string {
	paths := make([]string, 0, len(free))
	for p := range free {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		parts = append(parts, fmt.Sprintf("%.0f GB free under %s", free[p], p))
	}
	return strings.Join(parts, ", ")
}

// Tests for the policy's hold on a run: the account it prints, and the
// guard's answers before and after a run, without a real shortage.
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/headroom"
)

// TestAccount prints what a run took: the memory as it began and at the
// least, the swap grown, the parallelism (and what was asked when lowered),
// the costliest package without the module prefix, and the disk, sorted;
// and says when the memory was never read.
func TestAccount(t *testing.T) {
	h := &flakegate.Headroom{AvailableMB: 4300, LimitMB: 4500, MinAvailableMB: 1800, SwapGrowthMB: 120, ParallelAsked: 0, ParallelRan: 1,
		DiskFreeGB: map[string]float64{"/tmp": 749.6, "/home/me/src": 749.5},
		PackageMB:  map[string]float64{modulePrefix + "cmd/controller": 853, modulePrefix + "pkg/gns3": 18}, Samples: 10}
	got := account(h)
	for _, w := range []string{
		"4300 MB free as the run began (under a 4500 MB memory limit), 1800 MB at the least, swap grew 120 MB",
		"1 package(s) at once (asked go test's default of ",
		"the largest package cost 853 MB (cmd/controller)",
		"750 GB free under /home/me/src, 750 GB free under /tmp",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("the account does not say %q:\n%s", w, got)
		}
	}
	plain := account(&flakegate.Headroom{AvailableMB: 14000, MinAvailableMB: 12000, ParallelAsked: 4, ParallelRan: 4, Samples: 3})
	if strings.Contains(plain, "asked") || !strings.Contains(plain, "no package measured") || strings.Contains(plain, "limit") {
		t.Errorf("a run that was not lowered:\n%s", plain)
	}
	unread := account(&flakegate.Headroom{DiskFreeGB: map[string]float64{"/x": 9}})
	if !strings.HasPrefix(unread, "testgate: headroom: memory not read; 9 GB free under /x") {
		t.Errorf("a run whose memory was never read:\n%s", unread)
	}
	if pkg, mb := largestOf(map[string]float64{"b": 5, "a": 5, "c": 1}); pkg != "a" || mb != 5 {
		t.Errorf("largestOf on a tie = %s, %.0f; want the first by name", pkg, mb)
	}
	if pkg, _ := largestOf(nil); pkg != "" {
		t.Errorf("largestOf(nil) = %q", pkg)
	}
}

// TestGuard_StopsOnceAndAccountsForIt: a stop keeps its first reason, a
// run's events charge the packages running (and, in a divided package, its
// top-level tests under their own key) in memory and disk, and finish
// returns the account with the stop in it, packages and tests apart; a guard
// with no watch answers with no peaks.
func TestGuard_StopsOnceAndAccountsForIt(t *testing.T) {
	still := headroom.Memory{TotalMB: 8000, AvailableMB: 4000}
	disk := map[string]float64{"/x": 100}
	g := &guard{asked: 0, ran: 2, fit: 2, start: still, disk: disk,
		divided: func(pkg string) bool { return pkg == "x/e2e" },
		watch:   headroom.StartWatch(still, func() (headroom.Memory, error) { return still, nil }, time.Hour, func(string) {}),
		disks:   headroom.StartDiskWatch([]string{"/x"}, disk, func(string) (float64, error) { return 100, nil }, time.Hour)}
	g.Event(flakegate.Event{Action: "start", Package: "x/a"})
	g.Event(flakegate.Event{Action: "run", Package: "x/a", Test: "TestOne"})
	g.Event(flakegate.Event{Action: "start", Package: "x/e2e"})
	g.Event(flakegate.Event{Action: "run", Package: "x/e2e", Test: "TestGate"})
	g.Event(flakegate.Event{Action: "run", Package: "x/e2e", Test: "TestGate/case"})
	g.watch.Sample(headroom.Memory{TotalMB: 8000, AvailableMB: 3100})
	g.disks.Sample(map[string]float64{"/x": 99.5})
	g.Event(flakegate.Event{Action: "pass", Package: "x/a"})
	g.Event(flakegate.Event{Action: "pass", Package: "x/e2e", Test: "TestGate"})
	g.watch.Sample(headroom.Memory{TotalMB: 8000, AvailableMB: 2000})
	g.disks.Sample(map[string]float64{"/x": 98})
	g.stop("interrupted by terminated")
	g.stop("memory available 100 MB, under the 700 MB floor")
	if got := g.Stopped(); got != "interrupted by terminated" {
		t.Errorf("Stopped = %q, want the first reason", got)
	}
	if tests := g.testPeaks(); tests["x/e2e TestGate"] != 900 || len(tests) != 1 {
		t.Errorf("test peaks = %v, want TestGate alone at 900 (its subtest is its own)", tests)
	}
	if tests := g.testDiskPeaks(); tests["x/e2e TestGate"] != 512 {
		t.Errorf("test disk peaks = %v", tests)
	}
	h := g.finish()
	if h.PackageMB["x/a"] != 900 || h.PackageMB["x/e2e"] != 2000 || len(h.PackageMB) != 2 || h.MinAvailableMB != 2000 || h.Stopped != "interrupted by terminated" || h.ParallelRan != 2 || h.DiskFreeGB["/x"] != 100 || h.Samples != 2 {
		t.Errorf("finish = %+v", h)
	}
	if h.PackageDiskMB["x/a"] != 512 || h.PackageDiskMB["x/e2e"] != 2048 || h.MinDiskFreeGB["/x"] != 98 {
		t.Errorf("finish disk = %v, min %v", h.PackageDiskMB, h.MinDiskFreeGB)
	}
	if !strings.Contains(account(h), "the most disk written was 2048 MB (x/e2e)") {
		t.Errorf("the account does not say what was written:\n%s", account(h))
	}
	bare := &guard{asked: 3, ran: 3}
	if bare.peaks() != nil || bare.diskPeaks() != nil || bare.finish().Samples != 0 {
		t.Error("a guard with no watch has peaks or samples")
	}
}

// TestNewGuardBudget: a -budget-mb replaces the flat budget, so a light pass
// is lowered to what its own budget fits, never above what was asked and never
// under one.
func TestNewGuardBudget(t *testing.T) {
	g, err := newGuard(10, 1, []string{modulePrefix + "tools/internal/headroom"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := g.finish()
	if h.ParallelAsked != 10 || h.ParallelRan < 1 || h.ParallelRan > 10 {
		t.Errorf("asked 10 at 1 MB a package, ran %d", h.ParallelRan)
	}
	if h.AvailableMB > headroom.ReserveMB+10 && h.ParallelRan != 10 {
		t.Errorf("%.0f MB free fits ten 1 MB packages, ran %d", h.AvailableMB, h.ParallelRan)
	}
}

// TestNewGuard applies the policy here: the disk is read where the run
// builds, the parallelism is at least 1 and at most what was asked, and
// finish ends the watches with an account of this machine.
func TestNewGuard(t *testing.T) {
	g, err := newGuard(1, 0, []string{modulePrefix + "tools/internal/headroom"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := g.finish()
	if h.ParallelRan != 1 || h.ParallelAsked != 1 {
		t.Errorf("asked 1, ran %d", h.ParallelRan)
	}
	if len(h.DiskFreeGB) == 0 || len(h.MinDiskFreeGB) == 0 {
		t.Errorf("no disk was read: %+v", h)
	}
	if h.AvailableMB > 0 && h.MinAvailableMB <= 0 {
		t.Errorf("the memory was read but no minimum kept: %+v", h)
	}
}

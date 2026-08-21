package archtest

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The structural proof for Phase 23's "Adversarial Pattern Justification:
// prove election was consumed rather than reimplemented."
//
// An argument in a doc comment is not a proof. internal/schedule takes
// leadership as an injected `isLeader func() bool`, exactly as
// dispatch.Reaper already does, and a caller passes elector.IsLeader in the
// composition root. The consequence is checkable rather than asserted: a
// package that cannot see the election primitive at all cannot have
// rebuilt it, and cannot have grown a second lease loop beside it.
//
// This is the same enforcement shape render_test.go uses for the
// one-renderer rule, for the same reason: the ledger is a promise and the
// test is what keeps it true.

// electionPackages are the import paths that provide distributed
// leadership in this module, plus the primitive election is built on.
//
// internal/lock is included as well as internal/election because acquiring
// a lease directly off the lock manager is the other way to grow a second
// election: it is precisely what internal/election itself does, so a
// scheduler reaching for lock.Manager would be reimplementing the pattern
// one layer down rather than consuming it.
var electionPackages = []string{
	modulePath + "/internal/election",
	modulePath + "/internal/lock",
}

// schedulerPackages are the packages that must stay free of them.
var schedulerPackages = []string{
	modulePath + "/internal/schedule",
	modulePath + "/internal/schedule/rrule",
	modulePath + "/internal/schedule/zoneinfo",
}

// TestSchedulerNeverImportsElection asserts the scheduler consumes
// leadership rather than implementing it.
func TestSchedulerNeverImportsElection(t *testing.T) {
	offenders := make([]string, 0)

	// Deps rather than Imports: transitive reach is the property that
	// matters. A scheduler that pulled in election through a helper
	// package would satisfy a direct-import check while still carrying a
	// second lease loop into the binary.
	for _, pkg := range goList(t, false, modulePath+"/internal/schedule/...") {
		if !hasPrefix(pkg.ImportPath, schedulerPackages) {
			continue
		}
		for _, dep := range pkg.Deps {
			if hasPrefix(dep, electionPackages) {
				offenders = append(offenders, pkg.ImportPath+" reaches "+dep)
			}
		}
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf(
			"the scheduler has reached for a leadership primitive directly.\n"+
				"Phase 23 requires election to be CONSUMED, not rebuilt: internal/schedule.Scanner.Run takes an "+
				"`isLeader func() bool`, and cmd/controller passes elector.IsLeader into it, which is the same "+
				"seam dispatch.Reaper.Run already uses.\n"+
				"Importing internal/election or internal/lock here would mean a second lease loop in the same "+
				"binary, with its own key, its own TTL and its own failover behaviour to reason about.\n%v",
			offenders)
	}
}

// TestSchedulerRunTakesLeadershipAsAParameter is the positive half.
//
// The import test above can be satisfied by a scheduler that ignores
// leadership entirely, which would be worse than one that rebuilt it:
// every replica would scan, and the query load this design bounds would
// scale with replica count. This asserts the seam is actually present.
func TestSchedulerRunTakesLeadershipAsAParameter(t *testing.T) {
	src := readRepoFile(t, "internal", "schedule", "scanner.go")
	const want = "func (s *Scanner) Run(ctx context.Context, isLeader func() bool)"
	if !strings.Contains(src, want) {
		t.Errorf("internal/schedule/scanner.go no longer declares\n\t%s\n"+
			"That signature is the seam Phase 23's pattern gate is about: leadership arrives as a parameter, "+
			"so this package need not import internal/election to honour it.", want)
	}
}

// TestControllerWiresTheSchedulerToTheSchedulerLease asserts the other end
// of the seam is connected.
//
// A scheduler that takes an isLeader parameter and a controller that never
// passes it one is the failure this repository has recorded repeatedly: a
// complete, well-tested component that the running binary never reaches
// (FAILURE_PATTERNS.md #52, #110). cmd/controller has reserved
// schedulerLeaseKey since Phase 4 for exactly this.
func TestControllerWiresTheSchedulerToTheSchedulerLease(t *testing.T) {
	src := readRepoFile(t, "cmd", "controller", "main.go")
	for _, want := range []string{
		"schedulerLeaseKey",
		"schedule.NewScanner(",
		"elector.IsLeader",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("cmd/controller/main.go does not mention %q.\n"+
				"The scheduler must be constructed and run against the scheduler lease, or Phase 23 ships a "+
				"component the binary never reaches.", want)
		}
	}
}

// readRepoFile reads one source file relative to the repository root, so
// these assertions do not depend on the test's working directory.
func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	raw, err := os.ReadFile(path) // #nosec G304 -- a fixed, test-local path built from repoRoot
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

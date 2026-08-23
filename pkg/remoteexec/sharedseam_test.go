package remoteexec

import "testing"

// TestSnapshotForTest_HandsOutAFreshMemoAndPutsTheOldOneBack covers the
// seam a test needs when its subject is what a dial FAILURE looks like.
//
// The two halves are asserted separately because only one of them was
// obvious. Capturing the memo and restoring it is the part that reads like
// the other SnapshotForTest seams in this module. Emptying it is the part
// that makes the seam work at all: the map holds Runner POINTERS, so a
// version that captured without emptying handed the caller back the very
// same Runner, breaker counter and all, and the test it was written for
// went on failing.
func TestSnapshotForTest_HandsOutAFreshMemoAndPutsTheOldOneBack(t *testing.T) {
	opts := Options{KnownHostsPath: "seam-test-known-hosts"}

	original := Shared(opts)
	if again := Shared(opts); again != original {
		t.Fatal("Shared did not memoize: two calls with equal Options returned different Runners")
	}

	restore := SnapshotForTest()

	fresh := Shared(opts)
	if fresh == original {
		t.Error("Shared returned the memoized Runner after the snapshot, so its breaker state came with it")
	}

	restore()

	if back := Shared(opts); back != original {
		t.Error("restore did not put the original memo back")
	}
}

// TestSnapshotForTest_RestoreDropsWhatTheTestRegistered proves the other
// direction: a Runner built while the snapshot was held must not survive
// the restore, or one test's breaker state would leak into the next.
func TestSnapshotForTest_RestoreDropsWhatTheTestRegistered(t *testing.T) {
	opts := Options{KnownHostsPath: "seam-test-known-hosts-transient"}

	restore := SnapshotForTest()
	transient := Shared(opts)
	restore()

	if after := Shared(opts); after == transient {
		t.Error("a Runner created while the snapshot was held survived the restore")
	}

	// Leave the memo as this test found it, which is the seam doing its own
	// job on itself.
	SnapshotForTest()()
}

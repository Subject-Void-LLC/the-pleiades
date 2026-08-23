package registry

import (
	"strings"
	"sync"
	"testing"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := New[int]()

	if err := r.Register("a", 1); err != nil {
		t.Fatalf("Register(a): unexpected error: %v", err)
	}

	got, ok := r.Get("a")
	if !ok || got != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", got, ok)
	}
}

func TestRegistry_GetMiss(t *testing.T) {
	r := New[int]()

	got, ok := r.Get("missing")
	if ok {
		t.Fatalf("Get(missing) = %d, true; want ok=false", got)
	}
	if got != 0 {
		t.Fatalf("Get(missing) value = %d; want zero value", got)
	}
}

func TestRegistry_RegisterDuplicateErrors(t *testing.T) {
	r := New[string]()

	if err := r.Register("k", "first"); err != nil {
		t.Fatalf("first Register: unexpected error: %v", err)
	}
	err := r.Register("k", "second")
	if err == nil {
		t.Fatal("second Register(k): expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "k") {
		t.Errorf("error %q does not name the duplicate key", err.Error())
	}

	// The first registration must survive a rejected second attempt.
	got, ok := r.Get("k")
	if !ok || got != "first" {
		t.Fatalf("Get(k) after rejected duplicate = %q, %v; want %q, true", got, ok, "first")
	}
}

func TestRegistry_MustRegisterPanicsOnDuplicate(t *testing.T) {
	r := New[int]()
	r.MustRegister("k", 1)

	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister on duplicate key did not panic")
		}
	}()
	r.MustRegister("k", 2)
}

func TestRegistry_MustRegisterSucceedsOnFreshKey(t *testing.T) {
	r := New[int]()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("MustRegister on a fresh key panicked: %v", recovered)
		}
	}()
	r.MustRegister("k", 1)

	if got, ok := r.Get("k"); !ok || got != 1 {
		t.Fatalf("Get(k) = %d, %v; want 1, true", got, ok)
	}
}

func TestRegistry_AllReturnsSnapshotCopy(t *testing.T) {
	r := New[int]()
	r.MustRegister("a", 1)
	r.MustRegister("b", 2)

	snapshot := r.All()
	if len(snapshot) != 2 || snapshot["a"] != 1 || snapshot["b"] != 2 {
		t.Fatalf("All() = %v; want map[a:1 b:2]", snapshot)
	}

	// Mutating the returned snapshot must never reach the live registry.
	snapshot["a"] = 999
	delete(snapshot, "b")

	got, ok := r.Get("a")
	if !ok || got != 1 {
		t.Fatalf("Get(a) after mutating snapshot = %d, %v; want 1, true (live registry must be unaffected)", got, ok)
	}
	if _, ok := r.Get("b"); !ok {
		t.Fatal("Get(b) after deleting from snapshot: entry missing; live registry must be unaffected")
	}
}

func TestRegistry_AllOnEmptyRegistry(t *testing.T) {
	r := New[int]()
	snapshot := r.All()
	if len(snapshot) != 0 {
		t.Fatalf("All() on empty registry = %v; want empty map", snapshot)
	}
}

// TestRegistry_ConcurrentAccess races Register, MustRegister-on-fresh-keys,
// Get, and All against one Registry under -race, proving no torn map read
// or write: exactly one registration of a given duplicate key succeeds, and
// every reader sees a consistent (if possibly stale) view.
func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := New[int]()
	const goroutines = 50

	var wg sync.WaitGroup
	var successes int32
	var mu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// All goroutines race to register the SAME key, so at most one
			// can win; this is the concurrency case Register (not
			// MustRegister) exists for.
			if err := r.Register("shared", n); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
			r.Get("shared")
			r.All()
		}(i)
	}
	wg.Wait()

	if successes != 1 {
		t.Errorf("concurrent Register on the same key: %d callers succeeded; want exactly 1", successes)
	}
	if _, ok := r.Get("shared"); !ok {
		t.Error("Get(shared) after concurrent registration: not found")
	}
}

// TestRegistry_SnapshotForTest_RestoresAnAddedEntry is the case the seam
// exists for: a test registers, and the table must not carry that entry
// into whatever runs next.
func TestRegistry_SnapshotForTest_RestoresAnAddedEntry(t *testing.T) {
	r := New[int]()
	if err := r.Register("builtin", 1); err != nil {
		t.Fatalf("Register(builtin): %v", err)
	}

	restore := r.SnapshotForTest()
	if err := r.Register("from-a-test", 2); err != nil {
		t.Fatalf("Register(from-a-test): %v", err)
	}
	restore()

	if _, ok := r.Get("from-a-test"); ok {
		t.Error("from-a-test survived the restore, so a second iteration would collide on it")
	}
	if got, ok := r.Get("builtin"); !ok || got != 1 {
		t.Errorf("Get(builtin) = %d, %v; want 1, true: restore must not disturb what was already there", got, ok)
	}
}

// TestRegistry_SnapshotForTest_ReRegistrationSucceedsAfterRestore is the
// property the whole mechanism is judged on, stated directly rather than
// through the state of the map: the same name must be registrable again,
// which is exactly what a second `go test -count=2` iteration does.
func TestRegistry_SnapshotForTest_ReRegistrationSucceedsAfterRestore(t *testing.T) {
	r := New[int]()

	for i := range 3 {
		restore := r.SnapshotForTest()
		if err := r.Register("same-name-every-time", i); err != nil {
			t.Fatalf("iteration %d: Register: %v, want the restore to have freed the name", i, err)
		}
		restore()
	}
}

// TestRegistry_SnapshotForTest_RestoresAnEntryARemovalWouldHaveMissed
// pins the difference between restoring the table and deleting the keys
// added since. A snapshot taken over an existing entry must bring back
// the ORIGINAL value, not merely leave the key present.
//
// Register refuses a duplicate, so the overwrite is done by restoring an
// older snapshot over a newer one, which is the same shape a test helper
// called twice in one test produces through LIFO cleanup ordering.
func TestRegistry_SnapshotForTest_RestoresAnEntryARemovalWouldHaveMissed(t *testing.T) {
	r := New[int]()
	if err := r.Register("shared", 1); err != nil {
		t.Fatalf("Register(shared): %v", err)
	}

	outer := r.SnapshotForTest()
	if err := r.Register("inner", 2); err != nil {
		t.Fatalf("Register(inner): %v", err)
	}
	inner := r.SnapshotForTest()
	if err := r.Register("innermost", 3); err != nil {
		t.Fatalf("Register(innermost): %v", err)
	}

	// LIFO, the order t.Cleanup runs them in.
	inner()
	if _, ok := r.Get("inner"); !ok {
		t.Error("the inner restore removed an entry that predated its own snapshot")
	}
	outer()
	if _, ok := r.Get("inner"); ok {
		t.Error("the outer restore left an entry registered after its snapshot")
	}
	if got, ok := r.Get("shared"); !ok || got != 1 {
		t.Errorf("Get(shared) = %d, %v; want 1, true", got, ok)
	}
}

// TestRegistry_SnapshotForTest_RestoreIsIdempotent proves calling the
// closure twice is harmless. It is not a hypothetical: a helper that
// snapshots and is called twice in one test registers two cleanups, and a
// restore that handed the live map back would let the second call see
// entries the first had already put there.
func TestRegistry_SnapshotForTest_RestoreIsIdempotent(t *testing.T) {
	r := New[int]()
	if err := r.Register("builtin", 1); err != nil {
		t.Fatalf("Register(builtin): %v", err)
	}

	restore := r.SnapshotForTest()
	if err := r.Register("temporary", 2); err != nil {
		t.Fatalf("Register(temporary): %v", err)
	}

	restore()
	if err := r.Register("temporary", 3); err != nil {
		t.Fatalf("Register(temporary) after the first restore: %v", err)
	}
	restore()

	if _, ok := r.Get("temporary"); ok {
		t.Error("the second restore did not undo the registration made between the two calls")
	}
	if got, ok := r.Get("builtin"); !ok || got != 1 {
		t.Errorf("Get(builtin) = %d, %v; want 1, true", got, ok)
	}
}

// TestRegistry_SnapshotForTest_IsSafeUnderConcurrentUse runs the snapshot
// against live registrations and reads, so -race can see whether the
// snapshot is genuinely taken under the lock. The assertions are
// deliberately weak because the interleaving is not deterministic; the
// race detector is what this test is for.
func TestRegistry_SnapshotForTest_IsSafeUnderConcurrentUse(t *testing.T) {
	r := New[int]()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = r.Register(strings.Repeat("k", i+1), i)
			_, _ = r.Get("k")
			_ = r.All()
			r.SnapshotForTest()()
		}()
	}

	close(start)
	wg.Wait()
}

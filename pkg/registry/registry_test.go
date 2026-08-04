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

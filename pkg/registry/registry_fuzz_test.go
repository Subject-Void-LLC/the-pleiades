package registry

import "testing"

// FuzzRegistry drives Register/Get/All with arbitrary keys and values,
// including empty strings and keys containing arbitrary bytes, proving the
// registry never panics regardless of what a caller registers under.
func FuzzRegistry(f *testing.F) {
	f.Add("", 0)
	f.Add("a", 1)
	f.Add("a.b.c", -1)
	f.Add("\x00\xff", 42)

	f.Fuzz(func(t *testing.T, key string, value int) {
		r := New[int]()

		if err := r.Register(key, value); err != nil {
			t.Fatalf("Register on an empty registry unexpectedly failed: %v", err)
		}

		got, ok := r.Get(key)
		if !ok || got != value {
			t.Fatalf("Get(%q) = %d, %v; want %d, true", key, got, ok, value)
		}

		// Registering the same key again must fail, never panic or overwrite.
		if err := r.Register(key, value+1); err == nil {
			t.Fatalf("second Register(%q) succeeded; want a duplicate error", key)
		}
		if got, _ := r.Get(key); got != value {
			t.Fatalf("Get(%q) after a rejected duplicate = %d; want unchanged %d", key, got, value)
		}

		snapshot := r.All()
		if len(snapshot) != 1 || snapshot[key] != value {
			t.Fatalf("All() = %v; want exactly {%q: %d}", snapshot, key, value)
		}
	})
}

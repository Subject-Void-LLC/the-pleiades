// Package registry provides the Section 25 "typed generic Registry" shared
// primitive: one reusable, thread-safe, string-keyed lookup table for any
// value type, so device types, sync plugins, transports, launchable kinds,
// credential types, and every other Section 25 call site register into the
// same mechanism instead of each hand-rolling its own map.
//
// The [T any] type parameter here is Go's compile-time generics, not the
// interface{}/any AGENTS.md's typing rule warns against: every instantiation
// (Registry[Descriptor], Registry[func(Record) (InventoryItem, error)], ...)
// is fully statically typed, and Get/All never require a runtime type
// assertion at the call site the way a map[string]any would.
package registry

import (
	"fmt"
	"sync"
)

// Registry is a thread-safe, string-keyed lookup table for values of type T.
// The zero value is not usable; construct one with New.
type Registry[T any] struct {
	mu      sync.RWMutex
	entries map[string]T
}

// New creates an empty Registry.
func New[T any]() *Registry[T] {
	return &Registry[T]{entries: make(map[string]T)}
}

// MustRegister adds value under key, panicking if key is already
// registered. This is for compile-time-known registrations made from a
// package's own init() (mirroring pkg/capability's established
// panic-at-init convention): a duplicate built-in name is a programming
// error that must fail loudly at process start, never silently pick one
// registration over the other.
func (r *Registry[T]) MustRegister(key string, value T) {
	if err := r.Register(key, value); err != nil {
		panic("registry: " + err.Error())
	}
}

// Register adds value under key, returning an error if key is already
// registered. Unlike MustRegister, this never panics: it is for genuine
// runtime registration (a dynamically loaded sync plugin, a credential type
// read from config), where a duplicate is a data problem the caller should
// handle, not a process-ending programmer error.
func (r *Registry[T]) Register(key string, value T) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[key]; exists {
		return fmt.Errorf("duplicate registration for %q", key)
	}
	r.entries[key] = value
	return nil
}

// Get returns the value registered under key, and whether it was found.
func (r *Registry[T]) Get(key string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.entries[key]
	return v, ok
}

// SnapshotForTest captures this registry's current contents and returns a
// function that puts them back, so a test registering into a process-wide
// table can leave it the way it found it.
//
// It exists because a registry built at package scope outlives the test
// that writes to it. A second iteration under `go test -count=2` finds the
// first iteration's entries still present and fails on a duplicate
// registration, or panics outright when the registration went through
// MustRegister. That was true of seven packages in this module
// simultaneously, and no gate here ever saw it, because `go test` defaults
// to -count=1 and every CI target relies on that default.
//
// The restore replaces the whole table rather than deleting the keys added
// since, which is the stronger guarantee and the cheaper one to reason
// about: an entry a test overwrote comes back as it was, and one a test
// removed comes back at all. It is also safe to call more than once, since
// it copies out of the snapshot rather than handing the live map back.
//
// ForTest is in the name rather than in a _test.go file because a _test.go
// file cannot be imported across package boundaries, and most callers here
// need to isolate a table some OTHER package owns. internal/archtest
// forbids production code from calling anything by this name, which is the
// protection an export_test.go would otherwise have given for free.
func (r *Registry[T]) SnapshotForTest() func() {
	r.mu.RLock()
	saved := make(map[string]T, len(r.entries))
	for k, v := range r.entries {
		saved[k] = v
	}
	r.mu.RUnlock()

	return func() {
		restored := make(map[string]T, len(saved))
		for k, v := range saved {
			restored[k] = v
		}

		r.mu.Lock()
		defer r.mu.Unlock()
		r.entries = restored
	}
}

// All returns a snapshot copy of every registered entry, keyed by
// registration key. It is a copy specifically so a caller mutating the
// returned map (or a future registration racing with an in-flight caller
// iterating it) can never observe or cause a torn read of the live table.
func (r *Registry[T]) All() map[string]T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := make(map[string]T, len(r.entries))
	for k, v := range r.entries {
		snapshot[k] = v
	}
	return snapshot
}

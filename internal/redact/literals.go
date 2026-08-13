package redact

import (
	"sort"
	"sync"
)

// maxLiterals bounds how many distinct secret values one process will
// track.
//
// The set grows as jobs dispatch and shrinks only when a caller calls
// Forget, and a long-lived controller that never forgot anything would
// accumulate one entry per credential per launch for its whole uptime.
// That is both a memory leak and a slowdown on every masked line, since the
// scrub is linear in the size of the set.
//
// Past the bound, Add refuses new values rather than evicting old ones.
// Evicting would mean a secret that is still in flight silently stops being
// masked, which is the one failure this package exists to prevent. Refusing
// means a secret dispatched past the bound was never masked by the literal
// channel, which is bad, but it is bad loudly: Add reports it, and the key
// and pattern channels still apply. Ten thousand distinct live secrets in
// one process is far outside any real deployment, so reaching this is a
// signal that Forget is not being called rather than a capacity problem.
const maxLiterals = 10_000

// Literals is the set of exact secret values a process currently knows.
//
// This is the by-value masking channel, and it is code rather than data for
// a reason worth stating: a value worth masking is never known at build
// time and must never be committed to a file. rules.json therefore has no
// rule kind for a literal value, and this type is the third channel.
//
// A Literals is safe for concurrent use. The zero value is not usable;
// obtain one from Masker.Literals.
type Literals struct {
	mu sync.RWMutex
	m  map[string]struct{}

	// sorted caches Snapshot's result, rebuilt on the next read after a
	// mutation rather than on every read.
	//
	// This is not a micro-optimization. Snapshot is called once per masked
	// string, which is once per log line and once per captured command
	// output, and rebuilding plus re-sorting the whole set there made
	// masking a thousand live secrets cost 424 microseconds per log line
	// against a 968 nanosecond baseline. A masking control that expensive
	// is a masking control somebody eventually turns off, and one that is
	// off protects nothing. With the cache the sort happens once per
	// mutation instead of once per line.
	sorted []string
}

// newLiterals returns an empty set ready for concurrent use.
func newLiterals() *Literals {
	return &Literals{m: make(map[string]struct{})}
}

// Add records values as secret, so every later masked line scrubs them.
//
// A value shorter than MinLiteralLength is skipped rather than added, and
// the skip is reported. Adding a short or common value would scrub that
// substring out of every unrelated later line for the rest of the process,
// which corrupts output without protecting anything. Empty values are
// skipped for the same reason, more sharply: masking the empty string
// matches the gap between every pair of characters.
//
// The returned count is how many values were actually recorded. A caller
// that supplied one value and got zero back has learned that this secret
// will not be masked by the literal channel, which is worth logging.
func (l *Literals) Add(values ...string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	added := 0
	for _, v := range values {
		if len(v) < MinLiteralLength {
			continue
		}
		if _, exists := l.m[v]; exists {
			continue
		}
		if len(l.m) >= maxLiterals {
			// See maxLiterals: refusing is deliberate, and evicting would
			// silently stop masking a secret that is still in flight.
			break
		}
		l.m[v] = struct{}{}
		added++
	}
	if added > 0 {
		l.sorted = nil
	}
	return added
}

// Forget removes values from the set.
//
// A caller that knows a secret's useful life has ended (a job finished, a
// prompted password was consumed) should call this, so the set tracks what
// is live rather than everything that has ever passed through. Forgetting a
// value that was never added is a no-op.
//
// Forgetting is best effort as protection: any line already written is
// already written. It exists to bound the set, not to un-leak anything.
func (l *Literals) Forget(values ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, v := range values {
		delete(l.m, v)
	}
	l.sorted = nil
}

// snapshotShared returns the cached values, sorted longest first.
//
// The ordering is not cosmetic. The scrub in literal.go depends on longer
// secrets being masked before shorter ones, so that a password which is a
// prefix of a passphrase cannot carve the passphrase in half and leave its
// tail exposed. Returning the set already in that order means a caller
// cannot get it wrong.
//
// The returned slice is the cached one and callers must not modify it.
// It is unexported-package discipline rather than a copy on purpose: the
// only caller is Masker.Text, on the hot path, and copying there would
// reintroduce the per-line allocation the cache exists to remove. The
// exported Snapshot below returns a copy for everybody else.
func (l *Literals) snapshotShared() []string {
	l.mu.RLock()
	cached := l.sorted
	l.mu.RUnlock()
	if cached != nil {
		return cached
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	// Re-check: another goroutine may have rebuilt it while this one waited.
	if l.sorted != nil {
		return l.sorted
	}

	out := make([]string, 0, len(l.m))
	for v := range l.m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	l.sorted = out
	return out
}

// Snapshot returns a copy of the current values, sorted longest first.
func (l *Literals) Snapshot() []string {
	shared := l.snapshotShared()
	out := make([]string, len(shared))
	copy(out, shared)
	return out
}

// Len reports how many values are currently tracked. It exists so a caller
// can alert on the set approaching maxLiterals rather than discovering the
// refusal in a masked line that was not masked.
func (l *Literals) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.m)
}

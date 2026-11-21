// Package facts: a read of named keys of one device (Phase 41's read half),
// which is what a run's conditions ask of the Crawl CLI's store.
//
// # What a read costs
//
// Every line of a device's files is sealed whole, its key inside the seal
// (atrest.go), so no line can be matched to a key without opening it. A read
// of any keys of a device therefore opens every line of its history, every
// observation and its state file once: one AES-GCM open per line, then the
// sightings fold (seen.go). Lookup keeps only the named keys' current values,
// so it builds less than Current does, but it opens as much. A run reads each
// device once, at its first condition reading facts, for every key the run's
// conditions name, so the cost is paid once per device per run, never once
// per key or per task. The Controller's store can do better (its rows are
// found by key token), and a reader there should.
package facts

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Lookup is what a store holds of some named keys of one device.
type Lookup struct {
	// Current is each named key the device holds, at its current value (the
	// one Current would return for it). A named key it does not hold is not
	// here.
	Current map[string]Entry
	// Observers are the methods whose observations of the device the store
	// holds, sorted, so a reader can say which keys some method looked for
	// and did not find. A value stored before observations were kept names
	// its method too.
	Observers []string
}

// Lookup reads keys of device: their current values and the methods that
// have observed it. A device the store holds nothing for has an empty Lookup
// and no error. What it costs is the package's design note above.
func (s *FileStore) Lookup(_ context.Context, name string, keys []string) (Lookup, error) {
	d, err := s.read(name)
	if err != nil {
		return Lookup{}, err
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	out := Lookup{Current: currentOf(d, func(key string) bool { return want[key] })}
	methods := map[string]bool{}
	for _, o := range d.sf.seen {
		if o.By.Method != "" {
			methods[o.By.Method] = true
		}
	}
	for _, e := range d.written() {
		if e.By.Method != "" {
			methods[e.By.Method] = true
		}
	}
	for m := range methods {
		out.Observers = append(out.Observers, m)
	}
	sort.Strings(out.Observers)
	return out, nil
}

// currentOf is, for each of d's keys keep accepts, the value most recently
// confirmed, by Current's rule.
func currentOf(d device, keep func(key string) bool) map[string]Entry {
	current := map[string]Entry{}
	// In the order the values were written, stable and volatile together,
	// as the ent store reads its rows: where orders of several sources mix,
	// the comparison is not transitive and the order read decides.
	for _, e := range d.written() {
		if !keep(e.Key) {
			continue
		}
		if cur, ok := current[e.Key]; !ok || !isLater(cur.LastBy.Order, cur.LastSeenAt, e.LastBy.Order, e.LastSeenAt) {
			current[e.Key] = e
		}
	}
	return current
}

// HoldsAny reports whether the store holds any device's stored facts: a
// history, an observation or a state file. A directory a run made and never
// stored into, or one holding only lock files, holds none.
func (s *FileStore) HoldsAny() (bool, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("facts: %w", err)
	}
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".jsonl") || strings.HasSuffix(n, ".state.json") {
			return true, nil
		}
	}
	return false, nil
}

// Readable reports whether a value a reader was handed may be read under
// guard: its canonical JSON holds none of guard.Secrets and, when guard.Shaped
// is set, nothing shaped like a secret. The value's key and type were held to
// their rules when it was stored (Prepare); this is the part of the guard
// that moves, since a later run may know a secret the storing run did not. A
// value that does not encode is not readable.
func Readable(value any, guard Guard) bool {
	canon, err := Canonical(value)
	if err != nil || holdsSecret(canon, guard.Secrets) {
		return false
	}
	return guard.Shaped == nil || !guard.Shaped(string(canon))
}

// Package facts: what the Crawl CLI's file store keeps for one device, and
// how an observation changes it (Phase 41, 41b).
//
// <device>.jsonl holds the device's stable values, one line each, appended
// in the order they were stored and chained (chain.go). <device>.state.json
// holds what changes on every observation: when each stable value was last
// confirmed and by which run, and each volatile key's one value. It is
// rewritten whole through a uniquely named temporary file and a rename.
package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// line is one stored stable value as the history file holds it.
type line struct {
	Key        string     `json:"key"`
	Value      any        `json:"value"`
	Hash       string     `json:"hash"`
	ObservedAt time.Time  `json:"observed_at"`
	By         Provenance `json:"by,omitzero"`
	// Prev and Chain are the device's hash chain (chain.go).
	Prev  string `json:"prev"`
	Chain string `json:"chain,omitempty"`
}

// sighting is when a value was last confirmed, and by which run.
type sighting struct {
	At time.Time  `json:"at"`
	By Provenance `json:"by,omitzero"`
}

// volatileValue is a volatile key's one kept value.
type volatileValue struct {
	Value      any        `json:"value"`
	Hash       string     `json:"hash"`
	ObservedAt time.Time  `json:"observed_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	By         Provenance `json:"by,omitzero"`
	LastBy     Provenance `json:"last_by,omitzero"`
}

// state is <device>.state.json.
type state struct {
	// Seen is when each stable key's each value was last confirmed, by key
	// and then hash.
	Seen map[string]map[string]sighting `json:"seen"`
	// Volatile is each volatile key's value.
	Volatile map[string]volatileValue `json:"volatile"`
}

// Record implements Store, under the device's lock.
func (s *FileStore) Record(_ context.Context, device string, obs Observation) error {
	if len(obs.Entries) == 0 {
		return nil
	}
	return s.locked(device, func(r *os.Root) error {
		lines, complete, err := readLines(r, device)
		if err != nil {
			return err
		}
		st, err := readState(r, device)
		if err != nil {
			return err
		}
		at, by := obs.At.UTC(), obs.By
		entries := append([]Entry(nil), obs.Entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
		head := chainHead(lines)
		var appended []byte
		for _, e := range entries {
			if e.Kind == KindVolatile {
				recordVolatile(st, e, at, by)
				continue
			}
			if st.Seen[e.Key] == nil {
				st.Seen[e.Key] = map[string]sighting{}
			}
			if prior, ok := st.Seen[e.Key][e.Hash]; !ok || isLater(by.Order, at, prior.By.Order, prior.At) {
				st.Seen[e.Key][e.Hash] = sighting{At: at, By: by}
			}
			if cur, ok := valueAt(lines, e.Key, by.Order, at); ok && cur.Hash == e.Hash {
				continue
			}
			l := line{Key: e.Key, Value: e.Value, Hash: e.Hash, ObservedAt: at, By: by, Prev: head}
			if l.Chain, err = ChainHash(l.Prev, l.Key, l.Hash, l.ObservedAt, l.By); err != nil {
				return fmt.Errorf("facts: %w", err)
			}
			raw, err := json.Marshal(l)
			if err != nil {
				return fmt.Errorf("facts: %w", err)
			}
			appended = append(append(appended, raw...), '\n')
			lines = append(lines, l)
			head = l.Chain
		}
		if len(appended) > 0 {
			if err := appendLines(r, device, complete, appended); err != nil {
				return err
			}
		}
		return writeState(r, device, st)
	})
}

// recordVolatile keeps e as its key's one value when this observation is
// the latest of the key's, and otherwise leaves the newer value alone.
func recordVolatile(st state, e Entry, at time.Time, by Provenance) {
	cur, ok := st.Volatile[e.Key]
	switch {
	case ok && !isLater(by.Order, at, cur.LastBy.Order, cur.LastSeenAt):
		// A late observation of a value already replaced.
	case ok && cur.Hash == e.Hash:
		cur.LastSeenAt, cur.LastBy = at, by
		st.Volatile[e.Key] = cur
	default:
		st.Volatile[e.Key] = volatileValue{Value: e.Value, Hash: e.Hash, ObservedAt: at, LastSeenAt: at, By: by, LastBy: by}
	}
}

// isLater reports whether (a, ta) came after (b, tb): by run order where the
// two compare, else by the clock.
func isLater(a Order, ta time.Time, b Order, tb time.Time) bool {
	after, _ := Later(a, ta, b, tb)
	return after
}

// valueAt is the line key held as of (order, at): of the key's lines that
// came at or before it, the latest, the later line in the file on a tie.
func valueAt(lines []line, key string, order Order, at time.Time) (line, bool) {
	var found line
	ok := false
	for _, l := range lines {
		if l.Key != key || !notAfter(l.By.Order, l.ObservedAt, order, at) {
			continue
		}
		if !ok || !isLater(found.By.Order, found.ObservedAt, l.By.Order, l.ObservedAt) {
			found, ok = l, true
		}
	}
	return found, ok
}

// chainHead is the chain hash of the last chained line, "" when none is.
func chainHead(lines []line) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Chain != "" {
			return lines[i].Chain
		}
	}
	return ""
}

// Current implements Store: for each key, the value most recently confirmed,
// by run order where the runs compare (the later line on a tie); a volatile
// value takes part like any other, so a key whose kind changed reads its
// newest.
func (s *FileStore) Current(_ context.Context, device string) ([]Entry, error) {
	lines, st, err := s.read(device)
	if err != nil {
		return nil, err
	}
	current := map[string]Entry{}
	keep := func(e Entry) {
		if cur, ok := current[e.Key]; !ok || !isLater(cur.LastBy.Order, cur.LastSeenAt, e.LastBy.Order, e.LastSeenAt) {
			current[e.Key] = e
		}
	}
	for _, l := range lines {
		keep(stableEntry(st, l))
	}
	for key, v := range st.Volatile {
		keep(volatileEntry(key, v))
	}
	out := make([]Entry, 0, len(current))
	for _, e := range current {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// History implements Store: key's stable values in run order, and its
// volatile value last; each says when the clock rather than the run order
// placed it after the one before.
func (s *FileStore) History(_ context.Context, device, key string) ([]Entry, error) {
	lines, st, err := s.read(device)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, l := range lines {
		if l.Key == key {
			out = append(out, stableEntry(st, l))
		}
	}
	if v, ok := st.Volatile[key]; ok {
		out = append(out, volatileEntry(key, v))
	}
	return InRunOrder(out), nil
}

// Verify implements Store: device's chain, in the order its lines were
// written.
func (s *FileStore) Verify(_ context.Context, device string) (Verdict, error) {
	lines, _, err := s.read(device)
	if err != nil {
		return Verdict{}, err
	}
	links := make([]Link, len(lines))
	for i, l := range lines {
		links[i] = Link{Key: l.Key, Value: l.Value, Hash: l.Hash, ObservedAt: l.ObservedAt, By: l.By, Prev: l.Prev, Chain: l.Chain}
	}
	return VerifyChain(links), nil
}

// stableEntry is a history line as an Entry, with when and by which run its
// value was last confirmed.
func stableEntry(st state, l line) Entry {
	e := Entry{Key: l.Key, Value: l.Value, Hash: l.Hash, ObservedAt: l.ObservedAt, LastSeenAt: l.ObservedAt, Kind: KindStable, By: l.By, LastBy: l.By}
	if seen, ok := st.Seen[l.Key][l.Hash]; ok && isLater(seen.By.Order, seen.At, l.By.Order, l.ObservedAt) {
		e.LastSeenAt, e.LastBy = seen.At, seen.By
	}
	return e
}

// volatileEntry is a volatile value as an Entry.
func volatileEntry(key string, v volatileValue) Entry {
	return Entry{Key: key, Value: v.Value, Hash: v.Hash, ObservedAt: v.ObservedAt, LastSeenAt: v.LastSeenAt, Kind: KindVolatile, By: v.By, LastBy: v.LastBy}
}

// InRunOrder sorts a key's values by when they were first observed, by run
// order where two runs compare and by the clock where they do not, and marks
// each value the clock placed after the one before it. Both stores' histories
// use it.
func InRunOrder(entries []Entry) []Entry {
	sort.SliceStable(entries, func(i, j int) bool {
		after, _ := Later(entries[j].By.Order, entries[j].ObservedAt, entries[i].By.Order, entries[i].ObservedAt)
		return after
	})
	for i := range entries {
		entries[i].OrderedByClock = false
		if i > 0 {
			entries[i].OrderedByClock = !entries[i].By.Order.Comparable(entries[i-1].By.Order)
		}
	}
	return entries
}

// locked runs fn on the store's directory under device's lock.
func (s *FileStore) locked(device string, fn func(r *os.Root) error) error {
	r, err := s.root(device)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	lock, err := openLock(r, device+".lock")
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("facts: locking %s's facts: %w", device, err)
	}
	defer func() { _ = unlockFile(lock) }()
	return fn(r)
}

// read reads device's history and state, unlocked: a reader may see a
// value appended and not yet confirmed, never half a file.
func (s *FileStore) read(device string) ([]line, state, error) {
	r, err := s.root(device)
	if err != nil {
		return nil, state{}, err
	}
	defer func() { _ = r.Close() }()
	lines, _, err := readLines(r, device)
	if err != nil {
		return nil, state{}, err
	}
	st, err := readState(r, device)
	if err != nil {
		return nil, state{}, err
	}
	return lines, st, nil
}

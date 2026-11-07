// Package facts: what the Crawl CLI's file store keeps for one device, and
// how an observation changes it (Phase 41, 41b, 41h).
//
// <device>.jsonl holds the device's stable values, one line each, appended
// in the order they were stored and chained (chain.go). <device>.seen.jsonl
// holds one line per observation (file_seen.go), from which each value's last
// confirmation is read (Sightings). <device>.state.json holds each volatile
// key's one value; it is rewritten whole, through a uniquely named temporary
// file and a rename, only when a volatile value is replaced.
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
	// Seq is the observation that wrote it (Stored.Seq).
	Seq int64 `json:"seq,omitempty"`
	// Prev and Chain are the device's hash chain (chain.go).
	Prev  string `json:"prev"`
	Chain string `json:"chain,omitempty"`
}

// volatileValue is a volatile key's one kept value.
type volatileValue struct {
	Value      any        `json:"value"`
	Hash       string     `json:"hash"`
	ObservedAt time.Time  `json:"observed_at"`
	By         Provenance `json:"by,omitzero"`
	// Seq is the observation that wrote it.
	Seq int64 `json:"seq,omitempty"`
}

// state is <device>.state.json.
type state struct {
	// Volatile is each volatile key's value.
	Volatile map[string]volatileValue `json:"volatile"`
}

// Record implements Store, under the device's lock. It appends the values
// that changed and one observation, and rewrites the state only when a
// volatile value is replaced: nothing already written is edited.
func (s *FileStore) Record(_ context.Context, name string, obs Observation) error {
	if len(obs.Entries) == 0 {
		return nil
	}
	return s.locked(name, func(r *os.Root) error {
		d, err := load(r, name)
		if err != nil {
			return err
		}
		seq := d.nextSeq()
		at, by := StoredTime(obs.At), obs.By
		entries := append([]Entry(nil), obs.Entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
		head := chainHead(d.lines)
		// Each key's values, read once rather than the whole history once per
		// fact: a host's packages are thousands of keys against a history
		// that keeps growing.
		byKey := map[string][]Entry{}
		for _, e := range d.stable {
			byKey[e.Key] = append(byKey[e.Key], e)
		}
		var appended []byte
		// add chains l after the head and appends it.
		add := func(l line) error {
			l.Prev, l.Seq = head, seq
			var err error
			if l.Chain, err = ChainHash(name, l.Prev, l.Key, l.Hash, l.ObservedAt, l.By); err != nil {
				return fmt.Errorf("facts: %w", err)
			}
			raw, err := json.Marshal(l)
			if err != nil {
				return fmt.Errorf("facts: %w", err)
			}
			appended = append(append(appended, raw...), '\n')
			head = l.Chain
			return nil
		}
		replaced := false
		for _, e := range entries {
			if e.Kind == KindVolatile {
				if cur, ok := d.volatile[e.Key]; ok && (!isLater(by.Order, at, cur.LastBy.Order, cur.LastSeenAt) || cur.Hash == e.Hash) {
					// A late observation of a value already replaced, or the same
					// value again: the observation itself is the confirmation.
					continue
				}
				d.st.Volatile[e.Key] = volatileValue{Value: e.Value, Hash: e.Hash, ObservedAt: at, By: by, Seq: seq}
				replaced = true
				continue
			}
			prior := byKey[e.Key]
			if cur, ok := ValueAt(prior, by.Order, at); ok && cur.Hash == e.Hash {
				continue
			}
			if err := add(line{Key: e.Key, Value: e.Value, Hash: e.Hash, ObservedAt: at, By: by}); err != nil {
				return err
			}
			// A late change keeps the value confirmed after it in the
			// history (FAILURE_PATTERNS 923).
			if back, ok := Resumed(prior, Entry{Key: e.Key, Hash: e.Hash, ObservedAt: at, By: by}); ok {
				if err := add(line{Key: back.Key, Value: back.Value, Hash: back.Hash, ObservedAt: back.ObservedAt, By: back.By}); err != nil {
					return err
				}
			}
		}
		// The values first, then the state, then the observation that names
		// them: a crash between leaves values no observation covers, which
		// reads as the run never having confirmed anything. The reverse (an
		// observation kept and a state rename lost) needs the rename on disk
		// before the observation, which syncDir makes so where the platform
		// syncs a directory; on Windows it does nothing, and a power loss
		// there could credit a replaced volatile value with the newer
		// sighting.
		if len(appended) > 0 {
			if err := appendLines(r, name, d.complete, appended); err != nil {
				return err
			}
		}
		if replaced {
			if err := writeState(r, name, d.st); err != nil {
				return err
			}
		}
		return appendSeen(r, name, d.sf, seq, at, by, CoverageOf(entries))
	})
}

// isLater reports whether (a, ta) came after (b, tb): by run order where the
// two compare, else by the clock.
func isLater(a Order, ta time.Time, b Order, tb time.Time) bool {
	after, _ := Later(a, ta, b, tb)
	return after
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
func (s *FileStore) Current(_ context.Context, name string) ([]Entry, error) {
	d, err := s.read(name)
	if err != nil {
		return nil, err
	}
	current := map[string]Entry{}
	keep := func(e Entry) {
		if cur, ok := current[e.Key]; !ok || !isLater(cur.LastBy.Order, cur.LastSeenAt, e.LastBy.Order, e.LastSeenAt) {
			current[e.Key] = e
		}
	}
	// In the order the values were written, stable and volatile together,
	// as the ent store reads its rows: where orders of several sources mix,
	// the comparison is not transitive and the order read decides.
	for _, e := range d.written() {
		keep(e)
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
func (s *FileStore) History(_ context.Context, name, key string) ([]Entry, error) {
	d, err := s.read(name)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range d.stable {
		if e.Key == key {
			out = append(out, e)
		}
	}
	out = InRunOrder(out)
	if v, ok := d.volatile[key]; ok {
		out = append(out, v)
	}
	return out, nil
}

// Verify implements Store: device's chain, in the order its lines were
// written. It reads the history alone: the chain is all there, so a state
// file that does not decode does not stop a check of it.
func (s *FileStore) Verify(_ context.Context, device string) (Verdict, error) {
	r, err := s.root(device)
	if err != nil {
		return Verdict{}, err
	}
	defer func() { _ = r.Close() }()
	lines, _, err := readLines(r, device)
	if err != nil {
		return Verdict{}, err
	}
	links := make([]Link, len(lines))
	for i, l := range lines {
		links[i] = Link{Key: l.Key, Value: l.Value, Hash: l.Hash, ObservedAt: l.ObservedAt, By: l.By, Prev: l.Prev, Chain: l.Chain}
	}
	return VerifyChain(device, links), nil
}

// InRunOrder sorts a key's values by when they were first observed, by run
// order where two runs' orders compare and differ and by the clock where they
// do not, and marks each value the clock placed after the one before it. Both
// stores' histories use it. With orders of several sources mixed the
// comparison need not be transitive (the reviewer's F7), so the order among
// such values follows how they were stored; each such value is marked. A
// value that follows itself is one value and is read as one (mergeRepeats).
func InRunOrder(entries []Entry) []Entry {
	sort.SliceStable(entries, func(i, j int) bool {
		after, _ := Later(entries[j].By.Order, entries[j].ObservedAt, entries[i].By.Order, entries[i].ObservedAt)
		return after
	})
	for i := range entries {
		entries[i].OrderedByClock = false
		if i > 0 {
			_, entries[i].OrderedByClock = Later(entries[i].By.Order, entries[i].ObservedAt, entries[i-1].By.Order, entries[i-1].ObservedAt)
		}
	}
	return mergeRepeats(entries)
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

// read reads device's files, unlocked (load says in what order): a reader
// may see a value appended that no observation it read confirms, never half a
// file, and never a confirmation the writer did not make.
func (s *FileStore) read(name string) (device, error) {
	r, err := s.root(name)
	if err != nil {
		return device{}, err
	}
	defer func() { _ = r.Close() }()
	return load(r, name)
}

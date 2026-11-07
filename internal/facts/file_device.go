// Package facts: everything the Crawl CLI's file store holds for one device,
// read with each value's last confirmation (Phase 41h).
package facts

import (
	"os"
	"sort"
)

// device is everything stored for one device, with each value's last
// confirmation read from the observations.
type device struct {
	lines    []line
	complete int64
	st       state
	sf       seenFile
	// stable is each line as an entry, in file order; volatile each
	// volatile value, by key.
	stable   []Entry
	volatile map[string]Entry
}

// load reads device's files and their sightings.
func load(r *os.Root, name string) (device, error) {
	lines, complete, err := readLines(r, name)
	if err != nil {
		return device{}, err
	}
	st, err := readState(r, name)
	if err != nil {
		return device{}, err
	}
	sf, err := readSeen(r, name)
	if err != nil {
		return device{}, err
	}
	d := device{lines: lines, complete: complete, st: st, sf: sf, volatile: map[string]Entry{}}
	stored := make([]Stored, 0, len(lines)+len(st.Volatile))
	for _, l := range lines {
		stored = append(stored, Stored{Entry: Entry{Key: l.Key, Value: l.Value, Hash: l.Hash, ObservedAt: l.ObservedAt, Kind: KindStable, By: l.By}, Seq: l.Seq})
	}
	keys := make([]string, 0, len(st.Volatile))
	for key := range st.Volatile {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := st.Volatile[key]
		stored = append(stored, Stored{Entry: Entry{Key: key, Value: v.Value, Hash: v.Hash, ObservedAt: v.ObservedAt, Kind: KindVolatile, By: v.By}, Seq: v.Seq})
	}
	sighted := Sightings(stored, sf.seen)
	d.stable = sighted[:len(lines)]
	for _, e := range sighted[len(lines):] {
		d.volatile[e.Key] = e
	}
	return d, nil
}

// written is every value, stable and volatile, in the order written: by the
// observation that wrote it, and in file order within one.
func (d device) written() []Entry {
	type at struct {
		e   Entry
		seq int64
	}
	all := make([]at, 0, len(d.stable)+len(d.volatile))
	for i, e := range d.stable {
		all = append(all, at{e, d.lines[i].Seq})
	}
	for _, e := range d.volatile {
		all = append(all, at{e, d.st.Volatile[e.Key].Seq})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].seq < all[j].seq })
	out := make([]Entry, len(all))
	for i, a := range all {
		out[i] = a.e
	}
	return out
}

// nextSeq is the number the next observation takes: past every one stored,
// and past every value's, so a crash that kept a value but not its
// observation never has its number reused.
func (d device) nextSeq() int64 {
	n := d.sf.maxSeq
	for _, l := range d.lines {
		n = max(n, l.Seq)
	}
	for _, v := range d.st.Volatile {
		n = max(n, v.Seq)
	}
	return n + 1
}

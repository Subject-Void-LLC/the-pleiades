// Package facts: the Crawl CLI's record of each observation (Phase 41h).
//
// <device>.seen.jsonl is append-only: one line per observation the store
// recorded, naming its sequence number, when and by which run it was made, and
// the digest of what it covered. The first line naming a digest also lists
// its keys, so a coverage the device's gathers repeat is written once.
package facts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// seenLine is one observation as <device>.seen.jsonl holds it.
type seenLine struct {
	Seq int64      `json:"seq"`
	At  time.Time  `json:"at"`
	By  Provenance `json:"by,omitzero"`
	// Coverage is Coverage.Digest of what the observation covered; Keys is
	// that coverage itself, on the first line naming the digest only.
	Coverage string    `json:"coverage"`
	Keys     *Coverage `json:"keys,omitempty"`
}

// seenFile is what <device>.seen.jsonl holds: each observation, the coverage
// each digest names, and how many bytes of the file are whole lines.
type seenFile struct {
	seen     []Seen
	covers   map[string]*Coverage
	complete int64
	maxSeq   int64
}

// readSeen reads device's observations. A line naming a digest no line
// before it defined is an error: the defining line is always written first,
// in the same append.
func readSeen(r *os.Root, device string) (seenFile, error) {
	sf := seenFile{covers: map[string]*Coverage{}}
	name := device + ".seen.jsonl"
	complete, err := readJSONL(r, name, maxSeenLineBytes, func(n int, raw []byte) error {
		var l seenLine
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&l); err != nil || l.Seq <= 0 || l.Coverage == "" {
			return fmt.Errorf("facts: %s line %d is not a stored observation", name, n)
		}
		if l.Keys != nil {
			if l.Keys.Digest() != l.Coverage {
				return fmt.Errorf("facts: %s line %d lists keys its digest does not name", name, n)
			}
			sf.covers[l.Coverage] = l.Keys
		}
		covers, ok := sf.covers[l.Coverage]
		if !ok {
			return fmt.Errorf("facts: %s line %d names a coverage no line before it lists", name, n)
		}
		sf.seen = append(sf.seen, Seen{Seq: l.Seq, At: l.At, By: l.By, Covers: covers})
		sf.maxSeq = max(sf.maxSeq, l.Seq)
		return nil
	})
	if err != nil {
		return seenFile{}, err
	}
	sf.complete = complete
	return sf, nil
}

// appendSeen appends one observation, listing its coverage's keys when no
// line before it does.
func appendSeen(r *os.Root, device string, sf seenFile, seq int64, at time.Time, by Provenance, covers Coverage) error {
	digest := covers.Digest()
	l := seenLine{Seq: seq, At: at, By: by, Coverage: digest}
	if _, ok := sf.covers[digest]; !ok {
		l.Keys = &covers
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return appendTo(r, device+".seen.jsonl", sf.complete, append(raw, '\n'))
}

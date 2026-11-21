// Package facts: the Crawl CLI's record of each observation (Phase 41h).
//
// <device>.seen.jsonl is append-only: one line per observation the store
// recorded, naming its sequence number, when and by which run it was made, and
// the digest of what it covered. The first line naming a digest also lists
// its keys, so a coverage the device's gathers repeat is written once. Each
// line is sealed (atrest.go), so the keys it lists are not readable in the
// file.
package facts

import (
	"bytes"
	"encoding/json"
	"errors"
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
// each digest names, how many bytes of the file are whole lines, and how many
// whole lines could not be read and were left out.
type seenFile struct {
	seen     []Seen
	covers   map[string]*Coverage
	complete int64
	maxSeq   int64
	skipped  int
}

// readSeen reads device's observations. A whole line that does not decode,
// lists keys its digest does not name, or names a coverage no line before it
// lists is left out and counted, never an error: what an observation holds
// is when values were last confirmed, which the next run confirms again, so a
// damaged line costs some confirmations and refusing it would cost the
// device's facts, as a damaged state file once did (FAILURE_PATTERNS 922).
// Leaving one out can only make a value read as confirmed less recently: an
// observation never says a value changed. A line longer than any observation
// can be is still an error. A line that does not open under keys is damaged
// like any other and left out; having no keys at all is an error, since it
// would leave every line out and read as nothing ever confirmed.
func readSeen(r *os.Root, keys *Keys, device string) (seenFile, error) {
	sf := seenFile{covers: map[string]*Coverage{}}
	name := device + ".seen.jsonl"
	complete, err := readJSONL(r, name, maxSeenLineBytes, func(_ int, offset int64, sealed []byte) error {
		raw, err := openStored(keys, device, RoleSeen, offset, sealed)
		if errors.Is(err, ErrNoKeys) {
			return fmt.Errorf("facts: %s: %w", name, err)
		}
		if err != nil {
			sf.skipped++
			return nil
		}
		var l seenLine
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&l); err != nil || l.Seq <= 0 || l.Coverage == "" {
			sf.skipped++
			return nil
		}
		if l.Keys != nil {
			if l.Keys.Digest() != l.Coverage {
				sf.skipped++
				return nil
			}
			sf.covers[l.Coverage] = l.Keys
		}
		covers, ok := sf.covers[l.Coverage]
		if !ok {
			sf.skipped++
			return nil
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
// line before it does, sealed as the line at the file's end.
func appendSeen(r *os.Root, keys *Keys, device string, sf seenFile, seq int64, at time.Time, by Provenance, covers Coverage) error {
	digest := covers.Digest()
	l := seenLine{Seq: seq, At: at, By: by, Coverage: digest}
	if _, ok := sf.covers[digest]; !ok {
		l.Keys = &covers
	}
	plain, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	raw, err := keys.SealLine(device, RoleSeen, sf.complete, plain)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return appendTo(r, device+".seen.jsonl", sf.complete, append(raw, '\n'))
}

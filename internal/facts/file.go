// Package facts: the Crawl CLI's fact store, a directory of files under the
// project's .pleiades/facts, one pair per device.
//
// <device>.jsonl is append-only: one line per stored value, in the order they
// were observed, so a key's lines are its history and its last line is its
// current value. <device>.seen.json maps each key to when its current value
// was last confirmed, rewritten whole through a temporary file and a rename,
// since it changes on every observation and the history does not.
//
// Every file is opened through an os.Root on the directory, so a link planted
// there cannot lead a write outside it; the directory and its files are
// owner-only. Writers are one run at a time: the CLI holds .pleiades/run.lock
// for a run.
package facts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"time"
)

// File permissions: owner-only, as the journal's are.
const (
	dirMode  = 0o700
	fileMode = 0o600
)

// maxLineBytes bounds one line read back: a value's bound plus room for the
// key, hash and times.
const maxLineBytes = MaxValueBytes + 4<<10

// deviceName is what a device's file is named after: its ID, a UUID or a
// name made of the characters inventory allows.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// FileStore is the Crawl CLI's Store over a directory.
type FileStore struct {
	dir string
}

// NewFileStore returns a store over dir, made owner-only when missing.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

// line is one stored value as the history file holds it.
type line struct {
	Key        string    `json:"key"`
	Value      any       `json:"value"`
	Hash       string    `json:"hash"`
	ObservedAt time.Time `json:"observed_at"`
}

// root opens the directory as an os.Root, and checks device's name.
func (s *FileStore) root(device string) (*os.Root, error) {
	if !deviceName.MatchString(device) {
		return nil, fmt.Errorf("%w: %q is not a device a fact file may be named after", ErrInvalid, device)
	}
	r, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	return r, nil
}

// Record implements Store: each entry whose hash differs from its key's
// newest line is appended, and every observed key's last-seen time is set to
// at.
func (s *FileStore) Record(_ context.Context, device string, entries []Entry, at time.Time) error {
	if len(entries) == 0 {
		return nil
	}
	r, err := s.root(device)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	current, err := readCurrent(r, device)
	if err != nil {
		return err
	}
	seen, err := readSeen(r, device)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	var appended []byte
	for _, e := range entries {
		seen[e.Key] = at.UTC()
		if cur, ok := current[e.Key]; ok && cur.Hash == e.Hash {
			continue
		}
		raw, err := json.Marshal(line{Key: e.Key, Value: e.Value, Hash: e.Hash, ObservedAt: at.UTC()})
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		appended = append(append(appended, raw...), '\n')
	}
	if len(appended) > 0 {
		f, err := r.OpenFile(device+".jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		if _, err := f.Write(appended); err != nil {
			_ = f.Close()
			return fmt.Errorf("facts: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
	}
	return writeSeen(r, device, seen)
}

// Current implements Store.
func (s *FileStore) Current(_ context.Context, device string) ([]Entry, error) {
	r, err := s.root(device)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	current, err := readCurrent(r, device)
	if err != nil {
		return nil, err
	}
	seen, err := readSeen(r, device)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(current))
	for _, e := range current {
		e.LastSeenAt = seen[e.Key]
		if e.LastSeenAt.IsZero() {
			e.LastSeenAt = e.ObservedAt
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// History implements Store: every line for key, oldest first, each but the
// newest last seen when the next was observed.
func (s *FileStore) History(_ context.Context, device, key string) ([]Entry, error) {
	r, err := s.root(device)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	var out []Entry
	err = eachLine(r, device, func(l line) {
		if l.Key == key {
			out = append(out, Entry{Key: l.Key, Value: l.Value, Hash: l.Hash, ObservedAt: l.ObservedAt, LastSeenAt: l.ObservedAt})
		}
	})
	if err != nil {
		return nil, err
	}
	seen, err := readSeen(r, device)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].LastSeenAt = out[i+1].ObservedAt
		} else if t, ok := seen[key]; ok {
			out[i].LastSeenAt = t
		}
	}
	return out, nil
}

// readCurrent is the newest line of each key.
func readCurrent(r *os.Root, device string) (map[string]Entry, error) {
	current := map[string]Entry{}
	err := eachLine(r, device, func(l line) {
		current[l.Key] = Entry{Key: l.Key, Value: l.Value, Hash: l.Hash, ObservedAt: l.ObservedAt}
	})
	return current, err
}

// eachLine reads device's history file line by line. A missing file is an
// empty history; a line that does not decode is an error, never skipped.
func eachLine(r *os.Root, device string, fn func(line)) error {
	f, err := r.Open(device + ".jsonl")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLineBytes)
	n := 0
	for sc.Scan() {
		n++
		var l line
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.UseNumber()
		if err := dec.Decode(&l); err != nil || !ValidKey(l.Key) || l.Hash == "" {
			return fmt.Errorf("facts: %s.jsonl line %d is not a stored fact", device, n)
		}
		fn(l)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("facts: %s.jsonl: %w", device, err)
	}
	return nil
}

// readSeen reads device's last-seen times; a missing file is none.
func readSeen(r *os.Root, device string) (map[string]time.Time, error) {
	seen := map[string]time.Time{}
	f, err := r.Open(device + ".seen.json")
	if errors.Is(err, fs.ErrNotExist) {
		return seen, nil
	}
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := json.NewDecoder(f).Decode(&seen); err != nil {
		return nil, fmt.Errorf("facts: %s.seen.json: %w", device, err)
	}
	return seen, nil
}

// writeSeen rewrites device's last-seen times through a temporary file and a
// rename, so a reader never meets half of one.
func writeSeen(r *os.Root, device string, seen map[string]time.Time) error {
	raw, err := json.Marshal(seen)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	tmp := device + ".seen.json.tmp"
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return fmt.Errorf("facts: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	if err := r.Rename(tmp, device+".seen.json"); err != nil {
		return fmt.Errorf("facts: %w", err)
	}
	return nil
}

// Package scanignore reads a scan's ignore file: entries that suppress a
// finding by id, each saying why it is accepted and until when. An
// acceptance with no reason is one nobody can review, and one with no end
// is a finding nobody looks at again, so both are required; past its date
// an entry suppresses nothing, and the scan says so.
//
// The file is YAML, a list:
//
//   - id: GHSA-xxxx-xxxx-xxxx
//     reason: the vulnerable function is never called (see ADR 12)
//     until: 2026-12-31
package scanignore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// MaxFileBytes bounds an ignore file.
const MaxFileBytes = 1 << 20

// MaxEntries bounds how many entries one file holds.
const MaxEntries = 10000

// dateLayout is how until is written.
const dateLayout = "2006-01-02"

// Entry is one accepted finding.
type Entry struct {
	ID     string
	Reason string
	// Until is the last day the entry suppresses its finding, in UTC.
	Until time.Time
	// Line is where the entry starts in the file.
	Line int
}

// String names the entry for a message.
func (e Entry) String() string {
	return fmt.Sprintf("%s (line %d, until %s)", e.ID, e.Line, e.Until.Format(dateLayout))
}

// File is a parsed ignore file.
type File struct {
	Entries []Entry
}

// rawEntry is an entry as YAML holds it.
type rawEntry struct {
	ID     string `yaml:"id"`
	Reason string `yaml:"reason"`
	Until  string `yaml:"until"`
}

// Parse reads an ignore file, refusing it whole when any entry is
// incomplete, an id repeats, a key is unknown or a date does not read: a
// half-read ignore file would suppress some findings it was not meant to,
// or fail to suppress some it was.
func Parse(data []byte) (File, error) {
	if len(data) > MaxFileBytes {
		return File{}, fmt.Errorf("the ignore file is over %d bytes", MaxFileBytes)
	}
	if !utf8.Valid(data) {
		return File{}, errors.New("the ignore file is not UTF-8 text")
	}
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return File{}, nil
		}
		return File{}, fmt.Errorf("the ignore file is not YAML: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return File{}, errors.New("the ignore file holds more than one YAML document")
	}
	if len(root.Content) == 0 {
		return File{}, nil
	}
	list := root.Content[0]
	if list.Kind == yaml.ScalarNode && list.Tag == "!!null" {
		return File{}, nil
	}
	if list.Kind != yaml.SequenceNode {
		return File{}, errors.New("the ignore file is a list of entries, each with id, reason and until")
	}
	if len(list.Content) > MaxEntries {
		return File{}, fmt.Errorf("the ignore file holds %d entries, over %d", len(list.Content), MaxEntries)
	}
	var f File
	seen := map[string]int{}
	for i, node := range list.Content {
		if node.Kind != yaml.MappingNode {
			return File{}, fmt.Errorf("line %d: entry %d is not a mapping of id, reason and until", node.Line, i+1)
		}
		for k := 0; k+1 < len(node.Content); k += 2 {
			switch key := node.Content[k].Value; key {
			case "id", "reason", "until":
			default:
				return File{}, fmt.Errorf("line %d: %q is not a key an entry has (id, reason, until)", node.Content[k].Line, key)
			}
		}
		var raw rawEntry
		if err := node.Decode(&raw); err != nil {
			return File{}, fmt.Errorf("line %d: %w", node.Line, err)
		}
		e := Entry{ID: strings.TrimSpace(raw.ID), Reason: strings.TrimSpace(raw.Reason), Line: node.Line}
		switch {
		case e.ID == "":
			return File{}, fmt.Errorf("line %d: an entry has no id", node.Line)
		case e.Reason == "":
			return File{}, fmt.Errorf("line %d: %s has no reason; say why it is accepted", node.Line, e.ID)
		case strings.TrimSpace(raw.Until) == "":
			return File{}, fmt.Errorf("line %d: %s has no until; say when its acceptance ends (YYYY-MM-DD)", node.Line, e.ID)
		}
		until, err := time.Parse(dateLayout, strings.TrimSpace(raw.Until))
		if err != nil {
			return File{}, fmt.Errorf("line %d: %s's until %q is not a date (YYYY-MM-DD)", node.Line, e.ID, raw.Until)
		}
		e.Until = until
		if first, dup := seen[e.ID]; dup {
			return File{}, fmt.Errorf("line %d: %s is already ignored at line %d", node.Line, e.ID, first)
		}
		seen[e.ID] = node.Line
		f.Entries = append(f.Entries, e)
	}
	return f, nil
}

// Expired reports whether e no longer suppresses anything on day today:
// it suppresses through the whole of its until day.
func (e Entry) Expired(today time.Time) bool {
	y, m, d := today.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).After(e.Until)
}

// Judge applies a file to one scan's findings on one day.
type Judge struct {
	byID  map[string]Entry
	today time.Time
	used  map[string]bool
	all   []Entry
}

// Judge starts applying f on day today.
func (f File) Judge(today time.Time) *Judge {
	j := &Judge{byID: map[string]Entry{}, today: today, used: map[string]bool{}, all: f.Entries}
	for _, e := range f.Entries {
		j.byID[e.ID] = e
	}
	return j
}

// Ignored reports whether a finding known by any of ids is suppressed, and
// by which entry. An expired entry suppresses nothing.
func (j *Judge) Ignored(ids ...string) (Entry, bool) {
	for _, id := range ids {
		e, ok := j.byID[id]
		if !ok {
			continue
		}
		j.used[e.ID] = true
		if e.Expired(j.today) {
			continue
		}
		return e, true
	}
	return Entry{}, false
}

// Expired is every entry past its date, whether or not a finding met it.
func (j *Judge) Expired() []Entry {
	var out []Entry
	for _, e := range j.all {
		if e.Expired(j.today) {
			out = append(out, e)
		}
	}
	return out
}

// Unused is every entry still in date that no finding met: it can go.
func (j *Judge) Unused() []Entry {
	var out []Entry
	for _, e := range j.all {
		if !e.Expired(j.today) && !j.used[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

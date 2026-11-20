// Package journal: this file holds the journal file's format version.
//
// A Crawl-tier journal line is one engine.JournalEntry with one more key,
// "format", naming the version of the line's shape. The entry type itself
// does not carry it: the Walk tier stores the same entries in a database
// table whose shape its migrations already version, so the number belongs to
// the file, not to the entry.
package journal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// FormatVersion is the journal line format this build writes and the newest
// one it reads.
//
// Raise it when a line changes in a way an older reader would misread rather
// than refuse: a key whose meaning changes, an enum value an older build would
// take for another, a field an undo depends on. Adding a key an older reader
// does not know needs no raise of its own, since the strict reader already
// refuses an unknown key, but raising it anyway turns that refusal into one
// that names the version. docs/13-releases-and-stability.md states the rule.
//
// Format 2 (Phase 136b) adds the Collection identity ("collection") and the
// undo declaration's digest ("inverse_spec_digest"), which a rollback checks
// an undo against: fields an undo depends on, so the version is raised. A
// format-1 line reads with both empty.
//
// Format 3 (Phase 87) adds a task's rollback: steps as the run rendered them
// ("authored_steps"), which a later rollback replays instead of rendering the
// step again: a field an undo depends on, so the version is raised and a
// format-2 reader refuses the line by version rather than by an unknown key. A
// format-2 line reads with none, and a step of it that reads what only the run
// had is refused at rollback (internal/rollback's steps.go).
//
// Format 4 adds whether a call only read by its method's own declaration
// ("read_only_call"), which a rollback reads to leave a failed read out of
// what may have changed a device: a field a plan depends on, so the version is
// raised. A format-3 line reads with it false, which plans the failed call as
// an unknown effect, as that build did.
const FormatVersion = 4

// fileLine is the shape of one journal line: the entry, plus the format.
//
// The entry is embedded without a tag, so encoding/json writes its keys at
// the top level beside "format", and the strict reader's
// DisallowUnknownFields still applies to every one of them.
type fileLine struct {
	// Format is the line's format version. A line written before the field
	// existed has none, and is read as version 1, whose shape it has.
	Format int `json:"format"`

	// JournalEntry is the entry itself.
	engine.JournalEntry
}

// lineFor wraps an entry for writing at this build's format.
func lineFor(entry engine.JournalEntry) fileLine {
	return fileLine{Format: FormatVersion, JournalEntry: entry}
}

// checkFormat refuses a line whose format this build cannot read.
//
// An absent format (zero) is a line written before the field existed, which
// has version 1's shape. A version past FormatVersion was written by a newer
// build, and is refused by name rather than half read: an older reader that
// guessed would plan a rollback from fields it does not understand.
func checkFormat(format int) error {
	switch {
	case format < 0:
		return fmt.Errorf("names format %d, which is not a format version", format)
	case format > FormatVersion:
		return fmt.Errorf("is journal format %d, and this build reads formats up to %d: read it with the build that wrote it or a newer one", format, FormatVersion)
	}
	return nil
}

// peekFormat reads and validates only a line's "format" key before full
// decoding. An absent format (when line is not a JSON object or carries no
// "format" key) returns zero and nil so decodeEntry's strict decoder reports
// malformed entries and accepts pre-135a lines. An explicit null, 0,
// negative, newer, overflowing, or non-integer format is refused by name.
func peekFormat(line []byte) (int, error) {
	var head struct {
		// Format holds the raw JSON token for the "format" key.
		Format json.RawMessage `json:"format"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return 0, nil
	}
	raw := bytes.TrimSpace(head.Format)
	if len(raw) == 0 {
		return 0, nil
	}
	s := string(raw)
	if !isJSONInt(s) {
		return 0, fmt.Errorf("names a format that is not an integer format version")
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("names a format outside the integer range (this build reads formats up to %d)", FormatVersion)
	}
	if v == 0 {
		return 0, fmt.Errorf("names format 0, which is not a format version")
	}
	if err := checkFormat(v); err != nil {
		return 0, err
	}
	return v, nil
}

// isJSONInt reports whether s is a base-10 JSON integer with an optional
// leading minus sign and no fractional point, exponent, or quotes.
func isJSONInt(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Package journal: this file holds the journal file's format version.
//
// A Crawl-tier journal line is one engine.JournalEntry with one more key,
// "format", naming the version of the line's shape. The entry type itself
// does not carry it: the Walk tier stores the same entries in a database
// table whose shape its migrations already version, so the number belongs to
// the file, not to the entry.
package journal

import (
	"encoding/json"
	"fmt"

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
const FormatVersion = 2

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

// peekFormat reads only a line's "format" key, ignoring every other key, and
// returns zero when there is none or the line is not an object.
func peekFormat(line []byte) int {
	var head struct {
		// Format is the only key read.
		Format int `json:"format"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return 0
	}
	return head.Format
}

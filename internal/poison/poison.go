// Package poison decides which messages a Controller consumer acknowledges
// rather than hands back to be redelivered, because no delivery of them can
// ever be stored (FAILURE_PATTERNS 919). A Runner publishes run journal
// batches, job results and fact batches, so each is untrusted input, and a
// message that fails the same way every time it is delivered spends its
// delivery budget and holds up the messages behind it. The three consumers
// (internal/journal, internal/dispatch, internal/facts/entstore) share these
// checks so none of them can drift from the others (LESSONS 244).
package poison

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// The reasons CheckID gives. Each is a fixed string, so a log line built
// from one never repeats the ID it refused.
var (
	// ErrEmptyID is an ID with nothing in it.
	ErrEmptyID = errors.New("the id is empty")
	// ErrNotUTF8 is an ID that is not valid UTF-8, which a PostgreSQL text
	// column refuses (SQLSTATE 22021).
	ErrNotUTF8 = errors.New("the id is not valid UTF-8")
	// ErrControlCharacter is an ID holding a control character: a NUL, which
	// a PostgreSQL text column refuses (SQLSTATE 22021), or one that would
	// break a log line.
	ErrControlCharacter = errors.New("the id holds a control character")
)

// CheckID reports why id cannot name a job or a device in a message, or nil
// when it can: it must be non-empty, valid UTF-8 and free of control
// characters. No length is imposed: a device ID is the operator's, and the
// column holding it has no limit.
func CheckID(id string) error {
	switch {
	case id == "":
		return ErrEmptyID
	case !utf8.ValidString(id):
		return ErrNotUTF8
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return ErrControlCharacter
		}
	}
	return nil
}

// Unstorable returns the SQLSTATE when err is the database refusing a value
// in the message itself, so storing it again fails again, and whether it is.
// The code, never the server's message, is what a caller logs: some such
// messages quote the value refused.
func Unstorable(err error) (string, bool) {
	return ent.DataExceptionCode(err)
}

// Describe is what a log may say about err: its SQLSTATE when the database
// refused a value as data, whose message can quote the value, and err's own
// text otherwise.
func Describe(err error) string {
	if code, ok := Unstorable(err); ok {
		return "the database refused a value as data (SQLSTATE " + code + ")"
	}
	return err.Error()
}

// CleanText returns s with every NUL replaced by U+FFFD, for free text a
// device can supply (a job result's reason, from a device's own output),
// which a PostgreSQL text column refuses whole over one NUL. The rest of
// the text is kept as it was, so the record still says what happened.
func CleanText(s string) string {
	return strings.ReplaceAll(s, "\x00", string(utf8.RuneError))
}

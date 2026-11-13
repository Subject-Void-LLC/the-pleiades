// The time a grant records: which times a grant may hold, and reading one
// back from a record that is not trusted.
package inventory

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The years a grant's time may fall in: every year RFC 3339 writes with
// four digits, so every time a grant holds is one it reads back.
const (
	minGrantYear = 1
	maxGrantYear = 9999
)

// maxTimeInMessage bounds how much of a recorded time that will not parse
// is quoted into a grant's reason, since the record is not trusted.
const maxTimeInMessage = 64

// ValidateGrantTime refuses a time a grant cannot hold: none (the zero
// time), or one whose UTC year falls outside 1 to 9999, which RFC 3339
// cannot write in a form that reads back. Every write of a grant
// (record.Base.RecordGrant) is held to it, and GrantsFrom reads a recorded
// time that fails it as unreadable.
func ValidateGrantTime(t time.Time) error {
	if t.IsZero() {
		return errors.New("it records no time")
	}
	if year := t.UTC().Year(); year < minGrantYear || year > maxGrantYear {
		return fmt.Errorf("its time falls in the year %d, outside %d to %d", year, minGrantYear, maxGrantYear)
	}
	return nil
}

// grantTimeFrom reads a recorded grant's time, or says why it cannot: the
// time is missing, is not text, does not parse as RFC 3339, or is one
// ValidateGrantTime refuses. A time it cannot read is never the zero time
// passed off as one: the grant is reported unreadable instead.
func grantTimeFrom(entry map[string]any) (time.Time, string) {
	raw, present := entry["at"]
	if !present {
		return time.Time{}, "it records no time"
	}
	text, isText := raw.(string)
	if !isText {
		return time.Time{}, fmt.Sprintf("its time is a %T, not text", raw)
	}
	t, err := time.Parse(time.RFC3339, text)
	if err != nil {
		if len(text) > maxTimeInMessage {
			text = strings.ToValidUTF8(text[:maxTimeInMessage], "") + "..."
		}
		return time.Time{}, fmt.Sprintf("its time %q is not an RFC 3339 time", text)
	}
	if err := ValidateGrantTime(t); err != nil {
		return time.Time{}, err.Error()
	}
	return t.UTC(), ""
}

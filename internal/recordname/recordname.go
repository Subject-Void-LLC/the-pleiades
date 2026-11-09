// Package recordname holds the one rule a record's name obeys when it is
// chosen: it can be the record's address, and it shows as itself wherever
// the record is named.
//
// A device is addressed by its name in the API (/inventory/devices/{name})
// and the UI (/ui/devices/{name}), so its name is one URL path segment. No
// escaping makes "", "." and ".." one: url.PathEscape leaves "." and ".."
// as they are, every browser and path.Join resolve them (and their escaped
// forms %2E and %2E%2E) as steps between folders, and "" is no segment at
// all. A record holding one could be stored but never opened, changed or
// deleted by its address (FAILURE_PATTERNS 1011). Addressable is that rule.
//
// A device's name is also printed in every error, log line, report and page
// that names the device, and read by programs and models. A name holding a
// newline, an escape or a direction override would make each of those say
// something else ("sw2\npleiades: approved" prints a line of its own), so
// every one of them would have to defend itself. Check refuses such a name
// when it is chosen, which Addressable does not, since a reader asks
// Addressable of a name already stored.
//
// It is the one place the rule is written, for whatever builds such an
// address and whatever chooses a name that will become one. It imports
// only internal/termsafe, which imports nothing of this module, so any
// package under internal/ and cmd/ may use it. It is not under pkg/: no
// Collection needs it, and pkg/ is the surface a Collection compiles
// against.
package recordname

import (
	"errors"
	"fmt"
	"net/url"
)

// ErrUnusable is what Check's error matches under errors.Is: no record can
// be created under the name. A caller maps it to its own refusal (a 400, a
// form error, a skipped sync record) and shows Check's message, which says
// why.
var ErrUnusable = errors.New("not a name a record can be created under")

// Addressable reports whether name can be one URL path segment naming a
// record: anything but "", "." and "..".
//
// It is the rule a reader asks of a name already stored (whether to link
// it, whether a path can reach it), and it does not grow: a record stored
// under any other name stays reachable. A name being chosen is held to
// Check, which refuses more.
func Addressable(name string) bool {
	return name != "" && name != "." && name != ".."
}

// Segment is name escaped as one URL path segment, url.PathEscape's form,
// and with its first byte percent-encoded too when that form equals one of
// static: a fixed segment the router matches before a parameter at the same
// place, such as "new" beside /ui/{resource}/{id}.
//
// A router matches a static segment first, so the device named "new" linked
// as /ui/devices/new would open the create form. Linked as
// /ui/devices/%6Eew, the request has a RawPath, the router matches the
// escaped text against "new", finds it different, and hands it to {id},
// where api.PathParam decodes it back to "new". Such a name stays usable:
// it is a legitimate hostname. Segment does not judge Addressable; call it
// first.
func Segment(name string, static map[string]bool) string {
	seg := url.PathEscape(name)
	if !static[seg] {
		return seg
	}
	return fmt.Sprintf("%%%02X", seg[0]) + seg[1:]
}

// Check returns nil for a name a record may be created under, and otherwise
// an error that says why and matches ErrUnusable. It refuses what
// Addressable refuses, and a name that would not show as itself
// (ShowsAsItself): one holding invalid UTF-8, a control code, a line
// break, a format character, an invisible or blank character, a space
// other than U+0020, or spaces alone.
//
// Check is for the moment a name is chosen: a create, an import, a sync
// adding a record. A reader never holds a stored name to it. The rule
// reads Go's unicode tables, so it may grow with a Go release as well as
// with a release of this program, and a record stored before must keep
// being read, changed and deleted (LESSONS 415).
//
// Its message quotes the name only when it is "." or "..", and otherwise
// names a character by its code point and byte offset, so it never echoes
// anything else a caller sent.
func Check(name string) error {
	switch name {
	case "":
		return unusable{msg: "a name is required: an empty name cannot be the address of a record"}
	case ".", "..":
		return unusable{msg: fmt.Sprintf("the name %q cannot be used: a URL reads \".\" and \"..\" as steps between folders, however they are escaped, so no address could reach the record", name)}
	}
	if err := ShowsAsItself(name); err != nil {
		return unusable{msg: "the name cannot be used: " + err.Error() + " (a name is printed wherever the record is named, so it must be one line that shows as itself)"}
	}
	return nil
}

// unusable is Check's error: its text is the reason alone, and it unwraps
// to ErrUnusable so a caller can recognize it through any wrapping.
type unusable struct{ msg string }

// Error implements error.
func (e unusable) Error() string { return e.msg }

// Unwrap lets errors.Is match ErrUnusable.
func (e unusable) Unwrap() error { return ErrUnusable }

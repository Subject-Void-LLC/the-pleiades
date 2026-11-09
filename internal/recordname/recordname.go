// Package recordname holds the one rule a record's name obeys when the name
// is the record's address: it cannot be "", "." or "..".
//
// A device is addressed by its name in the API (/inventory/devices/{name})
// and the UI (/ui/devices/{name}), so its name is one URL path segment. No
// escaping makes these three names one: url.PathEscape leaves "." and ".."
// as they are, every browser and path.Join resolve them (and their escaped
// forms %2E and %2E%2E) as steps between folders, and "" is no segment at
// all. A record holding one could be stored but never opened, changed or
// deleted by its address (FAILURE_PATTERNS 1011).
//
// It is the one place the rule is written, for whatever builds such an
// address and whatever chooses a name that will become one. It imports
// nothing of this module, so any package under internal/ and cmd/ may
// use it. It is not under pkg/: no Collection needs it, and pkg/ is the
// surface a Collection compiles against.
package recordname

import (
	"errors"
	"fmt"
	"net/url"
)

// ErrUnaddressable is what Check's error matches under errors.Is: the name
// cannot be the address of a record. A caller maps it to its own refusal
// (a 400, a form error, a skipped sync record) and shows Check's message,
// which says why.
var ErrUnaddressable = errors.New("not a name a URL can carry")

// Addressable reports whether name can be one URL path segment naming a
// record: anything but "", "." and "..".
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

// Check returns nil for an Addressable name, and otherwise an error that
// says why the name cannot be used and matches ErrUnaddressable. Its
// message quotes the name only when it is "." or "..", so it never echoes
// anything else a caller sent.
func Check(name string) error {
	switch name {
	case "":
		return unaddressable{msg: "a name is required: an empty name cannot be the address of a record"}
	case ".", "..":
		return unaddressable{msg: fmt.Sprintf("the name %q cannot be used: a URL reads \".\" and \"..\" as steps between folders, however they are escaped, so no address could reach the record", name)}
	}
	return nil
}

// unaddressable is Check's error: its text is the reason alone, and it
// unwraps to ErrUnaddressable so a caller can recognize it through any
// wrapping.
type unaddressable struct{ msg string }

// Error implements error.
func (e unaddressable) Error() string { return e.msg }

// Unwrap lets errors.Is match ErrUnaddressable.
func (e unaddressable) Unwrap() error { return ErrUnaddressable }

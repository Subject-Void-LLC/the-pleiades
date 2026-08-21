// Package zoneinfo is the set of IANA time zones a schedule may name.
//
// It exists because the standard library will answer whether one specific
// name loads but will not enumerate the names that do, and this scheduler
// needs the list twice: once for the /zoneinfo endpoint an operator picks a
// zone from, and once as the save-time allowlist a schedule's timezone is
// checked against.
//
// Validating against a generated list rather than only against
// time.LoadLocation is a deliberate second control, not redundancy.
// LoadLocation's own name check is an implementation detail of the standard
// library, and it consults the host filesystem before the embedded archive
// -- so on a machine with a populated /usr/share/zoneinfo it will accept
// names this binary would not find elsewhere, which makes a schedule's
// validity depend on which host saved it. An explicit allowlist makes the
// answer the same everywhere.
//
//go:generate go run ../../../tools/genzoneinfo
package zoneinfo

import "time"

// index is the allowlist as a set, built once. A schedule save is a rare
// operation but a preview is not, and the API's own listing is served on
// every form load.
var index = func() map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}()

// Names returns every allowed zone name, sorted, as a fresh slice.
//
// A copy rather than the backing array: this is handed to an HTTP handler
// that marshals it, and a caller that sorted or truncated it in place would
// corrupt the allowlist for every later validation.
func Names() []string {
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// Valid reports whether name is an allowed zone.
func Valid(name string) bool {
	_, ok := index[name]
	return ok
}

// Load resolves name to a location, refusing anything outside the
// allowlist before consulting the standard library.
//
// The order matters. time.LoadLocation is given a name only after it has
// been matched against a known-good set, so no operator-supplied string
// ever reaches the loader's own path handling. That closes the injection
// shape a zone name has -- it is used to look something up by path -- at
// the boundary rather than relying on the loader's internal ".." check.
func Load(name string) (*time.Location, error) {
	if !Valid(name) {
		return nil, &UnknownZoneError{Name: name}
	}
	return time.LoadLocation(name)
}

// UnknownZoneError is returned by Load for a name outside the allowlist.
//
// It carries the rejected name so a form can attach the message to the
// field the operator typed it into, and is a distinct type so an API
// handler can turn it into a 400 rather than a 500 without matching on
// error text.
type UnknownZoneError struct {
	Name string
}

// Error implements error.
func (e *UnknownZoneError) Error() string {
	return "zoneinfo: " + e.Name + " is not a known IANA time zone"
}

// Common returns a short list of widely used zones, in the order an
// operator is most likely to want them, filtered to those this build
// actually has.
//
// It exists so a picker can offer something useful above six hundred
// alphabetical entries starting with Africa/Abidjan. It is a presentation
// convenience with no authority: Valid and Load consult the full list, and
// a zone absent from here is in no way second class.
func Common() []string {
	preferred := []string{
		"UTC",
		"America/New_York", "America/Chicago", "America/Denver",
		"America/Los_Angeles", "America/Sao_Paulo",
		"Europe/London", "Europe/Dublin", "Europe/Paris", "Europe/Berlin",
		"Europe/Madrid", "Europe/Amsterdam", "Europe/Stockholm",
		"Europe/Moscow",
		"Africa/Johannesburg", "Africa/Lagos", "Africa/Cairo",
		"Asia/Jerusalem", "Asia/Dubai", "Asia/Kolkata", "Asia/Singapore",
		"Asia/Hong_Kong", "Asia/Shanghai", "Asia/Tokyo", "Asia/Seoul",
		"Australia/Perth", "Australia/Sydney", "Pacific/Auckland",
	}
	out := make([]string, 0, len(preferred))
	for _, n := range preferred {
		if Valid(n) {
			out = append(out, n)
		}
	}
	return out
}

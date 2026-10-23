//go:build !unix

// Package engine: how a project file's mode is compared where modes are not
// Unix ones (Windows). Go's Chmod there uses only the owner write bit, to
// set or clear the read-only attribute, and Stat reports 0666 or 0444, so a
// file written 0600 never reads back as 0600; comparing every bit would
// call each run a change.
package engine

import "io/fs"

// modeHolds reports whether a file with mode have already carries what
// this system keeps of want: whether it is writable (the owner write bit,
// the read-only attribute's inverse).
func modeHolds(have, want fs.FileMode) bool {
	return have.Perm()&0o200 == want.Perm()&0o200
}

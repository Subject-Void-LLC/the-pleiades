//go:build unix

// Package engine: how a project file's mode is compared on a Unix system,
// which keeps every permission bit a write sets.
package engine

import "io/fs"

// modeHolds reports whether a file with mode have already carries the
// permission bits want: all nine, since a Unix file keeps each one.
func modeHolds(have, want fs.FileMode) bool {
	return have.Perm() == want.Perm()
}

//go:build unix || windows

// Package journal: where the run lock's lockFile is real, a held lock on
// an active run's record proves its run is alive.
package journal

// activeLocks says a lock on a record tells a live run from a dead one.
const activeLocks = true

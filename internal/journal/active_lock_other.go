//go:build !unix && !windows

// Package journal: where lockFile takes no lock (runlock_other.go), no
// record can be told dead, so every record reads as a live run and none is
// removed by another run.
package journal

// activeLocks says a lock on a record cannot tell a live run from a dead
// one here.
const activeLocks = false

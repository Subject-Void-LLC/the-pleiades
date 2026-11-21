// Package facts: what the external tests read of the file store's internals.
package facts

import (
	"os"
	"time"
)

// LockOpenRetries is how often openLock has tried again in this process.
func LockOpenRetries() int64 { return lockOpenRetries.Load() }

// OpenLock is openLock, for a test of a directory removed under it.
func OpenLock(r *os.Root, name string) (*os.File, error) { return openLock(r, name) }

// ChainInput is chainInput, the bytes one chain link is keyed over, for the
// golden test that holds them to what Phase 136b froze.
func ChainInput(device, prev, key, hash string, observedAt time.Time, by Provenance) ([]byte, error) {
	return chainInput(device, prev, key, hash, observedAt, by)
}

// testKeys is the at-rest keys the in-package tests seal under: factstest's
// own secret, so a file one wrote opens for the other.
func testKeys() *Keys {
	keys, err := NewKeys([]byte("facts-at-rest-test-secret-32byte"), "test")
	if err != nil {
		panic(err)
	}
	return keys
}

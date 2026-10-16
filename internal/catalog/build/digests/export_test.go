// Test hooks into package digests.
package digests

import "testing"

// SetAfterWrite runs f after each file a refresh writes, until t ends.
func SetAfterWrite(t testing.TB, f func(path string)) {
	afterWrite = f
	t.Cleanup(func() { afterWrite = nil })
}

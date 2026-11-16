// What the external tests of the key log reach inside the package: the
// platform rule, and the moment between a directory's check and the file's
// creation.
package devicetls

import "testing"

// UnsupportedOn is unsupportedOn, for a test of the platform rule on any
// platform.
var UnsupportedOn = unsupportedOn

// SetAfterKeyLogCheck runs hook between a key log directory's check and the
// file's creation for the rest of t, so a test can change the path in
// between.
func SetAfterKeyLogCheck(t testing.TB, hook func()) {
	t.Helper()
	afterKeyLogCheck = hook
	t.Cleanup(func() { afterKeyLogCheck = nil })
}

// Test seams for the external test package.
package inventory

// SetAfterHostsRead makes f run between a file repository read's two
// files, and returns what restores the default.
func SetAfterHostsRead(f func()) (restore func()) {
	old := afterHostsRead
	afterHostsRead = f
	return func() { afterHostsRead = old }
}

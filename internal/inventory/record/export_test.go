// Package record: what this package's own tests reach that is not
// exported.
package record

// SetGrantPolicyLineForTest decides the legacy grant rule as a tree on the
// release line line would, and returns a function putting it back.
func SetGrantPolicyLineForTest(line string) func() {
	old := grantPolicyLine
	grantPolicyLine = line
	return func() { grantPolicyLine = old }
}

// LegacyGrantsCount is legacyGrantsCount, for its table test.
var LegacyGrantsCount = legacyGrantsCount

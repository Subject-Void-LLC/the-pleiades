// Package snmp_test's TestMain runs the whole suite, the client tests
// against the in-process agent included, under goleak, so a client or an
// agent left running after its test fails the package rather than passing
// with a leak.
package snmp_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs every test in the package, then fails the run if any
// goroutine a test started is still alive.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

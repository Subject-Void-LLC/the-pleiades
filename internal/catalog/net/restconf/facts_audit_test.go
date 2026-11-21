// TestMain: every fact this package's methods emit in its tests is held to
// their declarations (pkg/collection/facttest).
package restconf_test

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
)

// TestMain runs the tests, then fails the binary for a fact a method emitted
// that it does not declare and for a fact-declaring method no test drove.
// The tests run against restconftest's in-process RESTCONF server over
// loopback TLS, so they run everywhere.
func TestMain(m *testing.M) {
	os.Exit(facttest.Main(m, facttest.Options{}))
}

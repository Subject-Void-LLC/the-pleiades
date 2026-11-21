// TestMain: every fact this package's methods emit in its tests is held to
// their declarations (pkg/collection/facttest).
package frr_test

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
)

// TestMain runs the tests, then fails the binary for a fact a method emitted
// that it does not declare and for a fact-declaring method no test drove.
// net.frr.facts is driven only against real FRR routers in containers
// (TestMethods_OnRealRouters), so without docker the coverage is reported.
func TestMain(m *testing.M) {
	os.Exit(facttest.Main(m, facttest.Options{Unavailable: testsupport.DockerMissing()}))
}

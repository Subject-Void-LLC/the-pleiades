// TestMain: every fact facts.gather emits in this package's tests is held to
// its declarations (pkg/collection/facttest).
package facts_test

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
)

// TestMain runs the tests, then fails the binary for a fact the method
// emitted that it does not declare and for a fact-declaring method no test
// drove. The POSIX path is driven through an in-process SSH server's /bin/sh,
// and the Windows path through a host model that answers each script, so on
// every machine some test makes facts.gather emit.
func TestMain(m *testing.M) {
	os.Exit(facttest.Main(m, facttest.Options{}))
}

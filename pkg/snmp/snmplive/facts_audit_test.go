// TestMain: every fact net.snmp.facts emits against net-snmp is held to its
// declaration (pkg/collection/facttest).
package snmplive

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
)

// TestMain runs the tests, then fails the binary for a fact a method of
// internal/catalog/net/snmp emitted that it does not declare, and for one of
// its fact-declaring methods these tests did not drive. They are the only
// tests that make net.snmp.facts emit, since its facts are kept only over
// authenticated SNMPv3, which the unit tests' in-process agent does not speak
// (that package's audit names this one in Options.DrivenBy, and
// internal/archtest holds the two to each other); they start a container, so
// without docker the coverage is reported.
func TestMain(m *testing.M) {
	os.Exit(facttest.Main(m, facttest.Options{
		Of:          "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/snmp",
		Unavailable: testsupport.DockerMissing(),
	}))
}

// TestMain: every fact this package's methods emit in its tests is held to
// their declarations (pkg/collection/facttest).
package snmp_test

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
)

// TestMain runs the tests, then fails the binary for a fact a method emitted
// that it does not declare and for a fact-declaring method no test drove.
// The tests run against snmptest's in-process agent over loopback UDP, so
// they run everywhere.
func TestMain(m *testing.M) {
	os.Exit(facttest.Main(m, facttest.Options{Exempt: map[string]string{
		"net.snmp.facts": "it keeps facts only over authenticated SNMPv3, and pkg/snmp/snmptest's agent speaks no SNMPv3; " +
			"pkg/snmp/snmplive's TestFactsGetAndWaitAgainstNetSNMP makes it emit against net-snmp, held to the declaration there",
	}}))
}

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
// they run everywhere. net.snmp.facts keeps facts only over authenticated
// SNMPv3, which that agent does not speak, so it is driven by
// pkg/snmp/snmplive's TestFactsGetAndWaitAgainstNetSNMP against net-snmp and
// held to its declaration there, whose TestMain names this package as Of
// (internal/archtest holds both ends).
func TestMain(m *testing.M) {
	os.Exit(facttest.Main(m, facttest.Options{DrivenBy: map[string]string{
		"net.snmp.facts": "github.com/Subject-Void-LLC/the-pleiades/pkg/snmp/snmplive",
	}}))
}

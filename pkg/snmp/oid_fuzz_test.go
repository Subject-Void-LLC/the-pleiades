// Fuzz tests for what a runbook hands the client: an OID and a SET's value
// never panic, and what they accept is canonical.
package snmp_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// FuzzParseOID: ParseOID never panics, and an OID it returns is returned
// unchanged when parsed again, so the client's own check of a numeric OID
// accepts every OID ParseOID gives.
func FuzzParseOID(f *testing.F) {
	for _, seed := range []string{"sysName.0", ".1.3.6.1.2.1.1.5.0", "SNMPv2-MIB::", "IF-MIB::ifDescr.2", "1..3", "0", "4294967296.1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		oid, err := snmp.ParseOID(text)
		if err != nil {
			return
		}
		again, err := snmp.ParseOID(oid)
		if err != nil || again != oid {
			t.Fatalf("ParseOID(%q) = %q, which parses again as %q, %v", text, oid, again, err)
		}
	})
}

// FuzzValue: a SET value of any type and text never panics, and an octet
// string accepted keeps its bytes whole.
func FuzzValue(f *testing.F) {
	f.Add("octet-string", "rack 4")
	f.Add("hex-string", "0x00ff")
	f.Add("integer", "-7")
	f.Add("ip-address", "192.0.2.1")
	f.Fuzz(func(t *testing.T, typ, text string) {
		v, err := snmp.Value("sysLocation.0", snmp.Type(typ), text)
		if err != nil {
			return
		}
		if typ == string(snmp.TypeOctetString) && string(v.Bytes) != text {
			t.Fatalf("Value kept %q of %q", v.Bytes, text)
		}
	})
}

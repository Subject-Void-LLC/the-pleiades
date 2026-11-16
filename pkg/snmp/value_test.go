// Tests for OIDs, values and masking as a runbook writes and a run records
// them.
package snmp_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// TestParseOID_NumericOrCuratedNamesOnly: an OID is numeric, or a name from
// the curated table with its instance, and anything else is refused rather
// than sent.
func TestParseOID_NumericOrCuratedNamesOnly(t *testing.T) {
	for in, want := range map[string]string{
		"1.3.6.1.2.1.1.5.0":            "1.3.6.1.2.1.1.5.0",
		".1.3.6.1.2.1.1.5.0":           "1.3.6.1.2.1.1.5.0",
		" sysName.0 ":                  "1.3.6.1.2.1.1.5.0",
		"SNMPv2-MIB::sysName.0":        "1.3.6.1.2.1.1.5.0",
		"IF-MIB::ifDescr.2":            "1.3.6.1.2.1.2.2.1.2.2",
		"system":                       snmp.OIDSystem,
		"ifName":                       "1.3.6.1.2.1.31.1.1.1.1",
		"0.0":                          "0.0",
		"1.3.6.1.4.1.9.9.1.4294967295": "1.3.6.1.4.1.9.9.1.4294967295",
	} {
		got, err := snmp.ParseOID(in)
		if err != nil || got != want {
			t.Errorf("ParseOID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "SNMPv2-MIB::", "1", "3.6.1", "1.3.6.1.4294967296", "1.3.06.1", "1..3", "1.3.x", "fooBar.0", "CISCO-MIB::x.0", "1.3.-1",
		strings.Repeat("1.", 128) + "1"} {
		if got, err := snmp.ParseOID(bad); err == nil {
			t.Errorf("ParseOID(%q) = %q, want a refusal", bad, got)
		}
	}
	if !snmp.Under("1.3.6.1.2.1.1.5.0", snmp.OIDSystem) || snmp.Under("1.3.6.1.2.1.10", "1.3.6.1.2.1.1") || !snmp.Under(snmp.OIDSystem, snmp.OIDSystem) {
		t.Error("Under does not follow sub-identifiers")
	}
	if names := snmp.Names(); len(names) < 30 || names[0] > names[1] {
		t.Errorf("Names = %v, want the sorted table", names)
	}
}

// TestValue_EveryWritableTypeAndItsBounds: a SET value is held to its type
// before anything is sent, and an octet string keeps its exact bytes.
func TestValue_EveryWritableTypeAndItsBounds(t *testing.T) {
	for _, tc := range []struct {
		t    snmp.Type
		in   string
		text string
		hex  bool
	}{
		{snmp.TypeInteger, "-2147483648", "-2147483648", false},
		{snmp.TypeGauge32, "4294967295", "4294967295", false},
		{snmp.TypeTimeTicks, "0", "0", false},
		{snmp.TypeOctetString, "Rack 4, row B", "Rack 4, row B", false},
		{snmp.TypeOctetString, "tab\there", "0x7461620968657265", true},
		{snmp.HexType, "0x001A2b", "0x001a2b", true},
		{snmp.HexType, "414243", "ABC", false},
		{snmp.TypeOID, "sysName.0", "1.3.6.1.2.1.1.5.0", false},
		{snmp.TypeIPAddress, "192.0.2.7", "192.0.2.7", false},
	} {
		v, err := snmp.Value("sysLocation.0", tc.t, tc.in)
		if err != nil || v.Text != tc.text || v.Hex != tc.hex || v.OID != snmp.OIDSysLocation {
			t.Errorf("Value(%s, %q) = %+v, %v; want text %q hex %v", tc.t, tc.in, v, err, tc.text, tc.hex)
		}
	}
	for _, tc := range []struct {
		t  snmp.Type
		in string
	}{
		{snmp.TypeInteger, "2147483648"}, {snmp.TypeInteger, "1.5"}, {snmp.TypeGauge32, "-1"}, {snmp.TypeTimeTicks, "4294967296"},
		{snmp.HexType, "0xzz"}, {snmp.HexType, "abc"}, {snmp.TypeOID, "nope"}, {snmp.TypeIPAddress, "2001:db8::1"}, {snmp.TypeIPAddress, "host"},
		{snmp.TypeCounter32, "1"}, {snmp.TypeCounter64, "1"}, {snmp.TypeNull, ""}, {"float", "1"},
		{snmp.TypeOctetString, strings.Repeat("x", 65536)},
	} {
		if v, err := snmp.Value("sysLocation.0", tc.t, tc.in); err == nil {
			t.Errorf("Value(%s, %q) = %+v, want a refusal", tc.t, tc.in, v)
		}
	}
	if _, err := snmp.Value("nope.0", snmp.TypeInteger, "1"); err == nil {
		t.Error("a value for an unknown name was built")
	}
	a, _ := snmp.Value("sysLocation.0", snmp.TypeOctetString, "ABC")
	b, _ := snmp.Value("sysLocation.0", snmp.HexType, "414243")
	c, _ := snmp.Value("sysLocation.0", snmp.TypeOctetString, "ABD")
	n, _ := snmp.Value("sysLocation.0", snmp.TypeGauge32, "7")
	if !snmp.Same(a, b) || snmp.Same(a, c) || snmp.Same(a, n) {
		t.Error("Same does not compare typed values")
	}
	if len(snmp.SettableTypes()) != 6 {
		t.Errorf("SettableTypes = %v", snmp.SettableTypes())
	}
}

// TestMask_TheCommunityAndTheAgentsSecretTables: a long secret is masked
// wherever it stands and a short community only as its own word, and the
// agent's community table is masked whatever it holds.
func TestMask_TheCommunityAndTheAgentsSecretTables(t *testing.T) {
	got := snmp.MaskValues("public republic long-secret-value public.", []string{"public", "long-secret-value"})
	if strings.Count(got, "public") != 1 || !strings.Contains(got, "republic") || strings.Contains(got, "long-secret-value") {
		t.Fatalf("MaskValues = %q", got)
	}
	row := snmp.Varbind{OID: "1.3.6.1.6.3.18.1.1.1.2.1", Type: snmp.TypeOctetString, Text: "another-community", Bytes: []byte("another-community")}
	if m := snmp.Mask(row, nil); strings.Contains(m.Text, "another") || m.Bytes != nil {
		t.Errorf("a community table value was kept: %+v", m)
	}
	descr := snmp.Varbind{OID: snmp.OIDSysDescr, Type: snmp.TypeOctetString, Text: "router with long-secret-value", Bytes: []byte("router with long-secret-value")}
	if m := snmp.Mask(descr, []string{"long-secret-value"}); strings.Contains(m.Text, "long-secret") || m.Bytes != nil || !strings.HasPrefix(m.Text, "router with") {
		t.Errorf("Mask = %+v", m)
	}
	n := snmp.Varbind{OID: "1.3.6.1.6.3.18.1.1.1.4.1", Type: snmp.TypeInteger, Text: "1"}
	if m := snmp.Mask(n, []string{"1"}); m.Text != n.Text || m.Type != n.Type {
		t.Error("a number was masked")
	}
	if !snmp.SecretOID("1.3.6.1.6.3.15.1.2.2.1.6.1") || snmp.SecretOID(snmp.OIDSysName) {
		t.Error("SecretOID does not follow the secret tables")
	}
}

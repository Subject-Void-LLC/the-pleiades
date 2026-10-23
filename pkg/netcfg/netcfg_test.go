// Tests of masking and of the layout Batfish reads FRR in.
package netcfg_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestMask replaces every secret form FRR and Cisco write, the same way for
// equal secrets, and leaves everything else where it was.
func TestMask(t *testing.T) {
	lines := []string{
		"password zebra",
		"enable password 8 abcd",
		" ip ospf authentication-key s1",
		" ip ospf message-digest-key 1 md5 s2",
		" ipv6 ospf6 authentication key-id 1 hash-algo sha256 key s3",
		" neighbor 10.0.0.2 password zebra",
		"key chain k",
		" key 1",
		"  key-string s4",
		" area-password md5 s5",
		"enable secret 9 $9$abc",
		"snmp-server community s6 RO",
		"crypto isakmp key s7 address 10.0.0.9",
		"username admin privilege 15 secret 5 s8",
		"password 123",
		" ip ospf authentication message-digest",
		"service password-encryption",
		"no password",
		"hostname r1",
	}
	masked := strings.Split(netcfg.Mask(strings.Join(lines, "\n")), "\n")
	for _, secret := range []string{"zebra", "abcd", "s1", "s2", "s3", "s4", "s5", "$9$abc", "s6", "s7", "s8", "123"} {
		for _, l := range masked {
			if slices.Contains(strings.Fields(l), secret) {
				t.Errorf("%s survived in %q", secret, l)
			}
		}
	}
	for i, kept := range map[int]string{
		1: "enable password 8 ", 3: " ip ospf message-digest-key 1 md5 ", 5: " neighbor 10.0.0.2 password ",
		7: " key 1", 11: " RO", 12: " address 10.0.0.9", 15: " ip ospf authentication message-digest",
		16: "service password-encryption", 17: "no password", 18: "hostname r1",
	} {
		if !strings.Contains(masked[i], kept) {
			t.Errorf("line %d lost %q: %q", i, kept, masked[i])
		}
	}
	if a, b := last(masked[0]), last(masked[5]); a != b || !strings.HasPrefix(a, netcfg.MaskPrefix) {
		t.Errorf("one secret masked as %q and %q", a, b)
	}
	if last(masked[0]) == last(masked[2]) {
		t.Error("two secrets masked alike")
	}
	if again := netcfg.Mask(strings.Join(masked, "\n")); again != strings.Join(masked, "\n") {
		t.Error("masking a masked configuration changed it")
	}
}

// last is a line's last word.
func last(line string) string {
	f := strings.Fields(line)
	return f[len(f)-1]
}

// TestFRRForBatfish lays a configuration out as Cumulus's concatenated
// file and moves each passive interface under router ospf, marking the
// interface whose block held the line and no other.
func TestFRRForBatfish(t *testing.T) {
	config := "frr version 10.1.1\nhostname r1\n!\ninterface eth1\n ip ospf area 0\nexit\n!\ninterface eth3\n ip ospf area 0\n ip ospf passive\nexit\n!\nrouter ospf\n ospf router-id 10.255.0.1\nexit\n!\nend\n"
	got, err := netcfg.FRRForBatfish("r1", config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "r1\n# This file describes the network interfaces\n\n# ports.conf --\n\nfrr version 10.1.1\n") {
		t.Errorf("layout:\n%s", got)
	}
	if strings.Contains(got, "ip ospf passive") || !strings.Contains(got, "router ospf\n passive-interface eth3\n ospf router-id") {
		t.Errorf("translation:\n%s", got)
	}
	if strings.Contains(got, "passive-interface eth1") {
		t.Error("eth1 was marked passive")
	}
	if _, err := netcfg.FRRForBatfish("r1", "interface eth3\n ip ospf passive\nexit\n"); err == nil {
		t.Error("a passive interface with no router ospf was accepted")
	}
	if netcfg.Hostname(config) != "r1" || netcfg.Hostname("frr version 10\n") != "" {
		t.Error("Hostname")
	}
}

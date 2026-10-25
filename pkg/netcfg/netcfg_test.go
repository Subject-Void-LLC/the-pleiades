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
		"crypto ikev2 keyring DMVPN-KEYRING",
		" peer ANY",
		"  address 0.0.0.0 0.0.0.0",
		"  pre-shared-key s9",
		"  pre-shared-key local 6 s10",
		"  pre-shared-key remote s11",
		"crypto ikev2 profile P",
		" authentication local pre-share key s12",
		" authentication remote pre-share",
		"crypto keyring K",
		" pre-shared-key address 10.0.0.9 key s13",
		"crypto key generate rsa general-keys label PLTEST modulus 2048",
		"crypto key zeroize rsa PLTEST",
		"% The key modulus size is 2048 bits",
		"crypto key import rsa K pem terminal s14",
		"crypto key export rsa K pem url tftp://10.0.0.9/k 3des s15",
		"key config-key password-encrypt s16",
		" passphrase s17",
		"crypto key import rsa K pem terminal",
	}
	masked := strings.Split(netcfg.Mask(strings.Join(lines, "\n")), "\n")
	for _, secret := range []string{"zebra", "abcd", "s1", "s2", "s3", "s4", "s5", "$9$abc", "s6", "s7", "s8", "123", "s9", "s10", "s11", "s12", "s13", "s14", "s15", "s16", "s17"} {
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
		21: "  address 0.0.0.0 0.0.0.0", 23: "  pre-shared-key local 6 ", 24: "  pre-shared-key remote ",
		26: " authentication local pre-share key ", 27: " authentication remote pre-share",
		29: " pre-shared-key address 10.0.0.9 key ",
		30: "crypto key generate rsa general-keys label PLTEST modulus 2048", 31: "crypto key zeroize rsa PLTEST",
		32: "% The key modulus size is 2048 bits",
		33: "crypto key import rsa K pem terminal ", 34: "crypto key export rsa K pem url tftp://10.0.0.9/k 3des ",
		35: "key config-key password-encrypt ", 37: "crypto key import rsa K pem terminal",
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

// TestMask_ByVendor holds every secret form these vendors write, in real
// configuration lines and the blocks around them, to the mask, and lines
// that hold none to staying as they are.
func TestMask_ByVendor(t *testing.T) {
	for _, tc := range []struct {
		config, secret string
	}{
		{"radius server r1\n address ipv4 10.0.0.9 auth-port 1812\n key 7 rad1usKey", "rad1usKey"},
		{"tacacs server t1\n address ipv4 10.0.0.8\n key tacKey9", "tacKey9"},
		{"radius-server key 7 0822455D0A16", "0822455D0A16"},
		{"interface Gi1\n standby 1 authentication hsrpTxt", "hsrpTxt"},
		{"interface Gi1\n standby 1 authentication md5 key-string hsrpMd5", "hsrpMd5"},
		{"interface Gi1\n vrrp 1 authentication text vrrpTxt", "vrrpTxt"},
		{"interface Gi1\n glbp 1 authentication md5 key-string glbpMd5", "glbpMd5"},
		{"interface eth1\n ip rip authentication string ripStr", "ripStr"},
		{"interface Tunnel0\n ip nhrp authentication nhrpKey", "nhrpKey"},
		{"snmp-server host 10.0.0.1 version 2c commV2", "commV2"},
		{"snmp-server host 10.0.0.1 traps version 1 commV1", "commV1"},
		{"snmp-server host 10.0.0.1 commNoVer", "commNoVer"},
		{"snmp-server community commRO RO", "commRO"},
		{"key chain k\n key 1\n  key-string chainKey", "chainKey"},
		{"router bgp 65001\n neighbor 10.0.0.2 password bgpPw", "bgpPw"},
		{"interface eth1\n ip ospf message-digest-key 1 md5 ospfMd5", "ospfMd5"},
		{"username admin privilege 15 secret 9 $9$hashval", "$9$hashval"},
	} {
		masked := netcfg.Mask(tc.config)
		if strings.Contains(masked, tc.secret) || !strings.Contains(masked, netcfg.MaskPrefix) {
			t.Errorf("%q kept its secret:\n%s", tc.config, masked)
		}
	}
	for _, kept := range []string{
		"key chain k\n key 1\n  accept-lifetime infinite",
		"interface eth1\n ip ospf authentication message-digest",
		"interface eth1\n ip ospf authentication null",
		"interface eth1\n ip rip authentication mode md5",
		"interface eth1\n isis authentication key-chain k",
		"snmp-server host 10.0.0.1 version 3 priv v3user",
		"interface Gi1\n standby 1 authentication md5 key-chain hsrp-chain",
	} {
		if netcfg.Mask(kept) != kept {
			t.Errorf("%q was masked:\n%s", kept, netcfg.Mask(kept))
		}
	}
}

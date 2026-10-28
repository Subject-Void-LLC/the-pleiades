// Tests of the secrets that are not one word: IOS's clear-text secrets,
// which run to the end of their line, and the lines that hold two.
package netcfg_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestMask_ClearTextRunsToTheEndOfTheLine holds every IOS XE line shape
// that takes a clear-text secret, with spaces inside it, to leaving no word
// of the secret behind, through both Mask and MaskChecked. Every word of
// each secret is a made-up token that appears nowhere else in its line.
func TestMask_ClearTextRunsToTheEndOfTheLine(t *testing.T) {
	for _, tc := range []struct {
		config, secret, kept string
	}{
		{"username admin password 0 qa1 qa2", "qa1 qa2", "username admin password 0 "},
		{"username admin privilege 15 secret 0 qb1 qb2 qb3", "qb1 qb2 qb3", "username admin privilege 15 secret 0 "},
		{"username admin password qc1 qc2", "qc1 qc2", "username admin password "},
		{"enable password 0 qd1 qd2", "qd1 qd2", "enable password 0 "},
		{"enable password level 15 qe1 qe2", "qe1 qe2", "enable password level 15 "},
		{"line vty 0 4\n password qf1 qf2", "qf1 qf2", " password "},
		{"tacacs-server key 0 qg1 qg2", "qg1 qg2", "tacacs-server key 0 "},
		{"radius-server key qh1 qh2", "qh1 qh2", "radius-server key "},
		{"tacacs server t1\n address ipv4 10.0.0.8\n key 0 qi1 qi2", "qi1 qi2", " key 0 "},
		{"radius server r1\n key qj1 qj2", "qj1 qj2", " key "},
		{"radius-server host 10.0.0.9 auth-port 1812 acct-port 1813 key 0 qk1 qk2", "qk1 qk2", " acct-port 1813 key 0 "},
		{"aaa group server tacacs+ g\n server-private 10.0.0.9 key 0 ql1 ql2", "ql1 ql2", " server-private 10.0.0.9 key 0 "},
		{"router bgp 65001\n neighbor 10.0.0.2 password 0 qm1 qm2", "qm1 qm2", " neighbor 10.0.0.2 password 0 "},
		{"interface Serial0\n ppp chap password 0 qn1 qn2", "qn1 qn2", " ppp chap password 0 "},
		{"interface Serial0\n ppp pap sent-username u1 password 0 qo1 qo2", "qo1 qo2", " sent-username u1 password 0 "},
		{"dot11 ssid lab\n wpa-psk ascii 0 qp1 qp2", "qp1 qp2", " wpa-psk ascii 0 "},
		{"wlan lab 1 lab\n security wpa psk set-key ascii 0 qq1 qq2", "qq1 qq2", " set-key ascii 0 "},
		{"crypto ikev2 keyring k\n peer p\n  pre-shared-key local qr1 qr2", "qr1 qr2", "  pre-shared-key local "},
		{"crypto keyring k\n pre-shared-key address 10.0.0.9 key qs1 qs2", "qs1 qs2", " pre-shared-key address 10.0.0.9 key "},
		{"key config-key password-encrypt qt1 qt2", "qt1 qt2", "key config-key password-encrypt "},
		{"l2tp-class c\n password 0 qu1 qu2", "qu1 qu2", " password 0 "},
		{"interface Gi1\n ip ospf authentication-key qv1 qv2", "qv1 qv2", " ip ospf authentication-key "},
		{"key chain k\n key 1\n  key-string 0 qw1 qw2", "qw1 qw2", "  key-string 0 "},
		// A carriage return the device ended the line with stays.
		{"username admin password 0 qx1 qx2\r", "qx1 qx2", "username admin password 0 "},
		// An SNMPv3 user's two passwords, each one word.
		{"snmp-server user u1 g1 v3 auth sha qy1 priv aes 128 qy2", "qy1 qy2", "snmp-server user u1 g1 v3 auth sha "},
		{"snmp-server user u1 g1 v3 encrypted auth md5 qz1 priv des qz2 access 10", "qz1 qz2", " priv des "},
		// An OSPFv3 IPsec SPI's keys.
		{"interface Gi1\n ipv6 ospf authentication ipsec spi 500 md5 0 0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef", " ipv6 ospf authentication ipsec spi 500 md5 0 "},
		{"interface Gi1\n ospfv3 encryption ipsec spi 1001 esp aes-cbc 128 0 fedcba9876543210fedcba9876543210 sha1 0 00112233445566778899aabbccddeeff00112233", "fedcba9876543210fedcba9876543210 00112233445566778899aabbccddeeff00112233", " ospfv3 encryption ipsec spi 1001 esp aes-cbc 128 0 "},
		// An ipsec-manual crypto map entry's session keys, every form of IOS
		// XE's set session-key.
		{"crypto map m 10 ipsec-manual\n set session-key inbound ah 300 1111111111111111111111111111111111111111", "1111111111111111111111111111111111111111", " set session-key inbound ah 300 "},
		{"crypto map m 10 ipsec-manual\n set session-key outbound esp 256 cipher abcdefabcdefabcd", "abcdefabcdefabcd", " set session-key outbound esp 256 cipher "},
		{"crypto map m 10 ipsec-manual\n set session-key inbound esp 4294967295 cipher 0123456789012345 authenticator 98765432109876549876543210987654", "0123456789012345 98765432109876549876543210987654", " set session-key inbound esp 4294967295 cipher "},
		{"crypto map ipv6 v6 ipsec-manual\n set session-key outbound esp 256 authenticator fedcbafedcbafedcfedcbafedcbafedc", "fedcbafedcbafedcfedcbafedcbafedc", " set session-key outbound esp 256 authenticator "},
	} {
		masked := netcfg.Mask(tc.config)
		checked, found, err := netcfg.MaskChecked(tc.config)
		if err != nil {
			t.Errorf("%q: MaskChecked refused it: %v", tc.config, err)
		}
		if checked != masked {
			t.Errorf("%q: MaskChecked and Mask disagree:\n%s\n%s", tc.config, checked, masked)
		}
		for _, part := range strings.Fields(tc.secret) {
			if strings.Contains(masked, part) {
				t.Errorf("%q kept %q:\n%s", tc.config, part, masked)
			}
		}
		if !strings.Contains(masked, tc.kept) || !strings.Contains(masked, netcfg.MaskPrefix) {
			t.Errorf("%q lost %q:\n%s", tc.config, tc.kept, masked)
		}
		if strings.HasSuffix(tc.config, "\r") != strings.HasSuffix(masked, "\r") {
			t.Errorf("%q: the line's ending changed:\n%q", tc.config, masked)
		}
		if again := netcfg.Mask(masked); again != masked {
			t.Errorf("%q: masking a masked configuration changed it:\n%s", tc.config, again)
		}
		// The secret is learned whole, spaces and all, so a run masks it by
		// value wherever else it shows.
		if len(strings.Fields(tc.secret)) == 1 || strings.HasPrefix(tc.config, "snmp-server user") || strings.Contains(tc.config, "ipsec") {
			continue
		}
		if len(found) != 1 || found[0] != tc.secret {
			t.Errorf("%q: found %q, want [%q]", tc.config, found, tc.secret)
		}
	}
}

// TestMask_OneWordSecretsKeepWhatFollows holds the commands whose secret is
// one word that more of the command follows (an SNMP community, an NTP key,
// a crypto isakmp key, an IS-IS password, a typed secret) to masking that
// word and keeping the rest, and lines that hold no secret to staying as
// they are.
func TestMask_OneWordSecretsKeepWhatFollows(t *testing.T) {
	for _, tc := range []struct {
		config, secret, kept string
	}{
		{"snmp-server community pa1 RO 10", "pa1", " RO 10"},
		{"ntp authentication-key 1 md5 pb1 7", "pb1", " 7"},
		{"crypto isakmp key 0 pc1 address 10.0.0.9 no-xauth", "pc1", " address 10.0.0.9 no-xauth"},
		{"crypto isakmp key pd1 hostname peer.example.net", "pd1", " hostname peer.example.net"},
		{"router isis\n area-password pe1 authenticate snp validate", "pe1", " authenticate snp validate"},
		{"router isis\n domain-password pf1 authenticate snp send-only", "pf1", " authenticate snp send-only"},
		{"interface Gi1\n isis password pg1 level-1", "pg1", " level-1"},
		{"interface Gi1\n standby 1 authentication md5 key-string 7 ph1 timeout 30", "ph1", " timeout 30"},
		{"tacacs-server key 7 pi1", "pi1", "tacacs-server key 7 "},
	} {
		masked := netcfg.Mask(tc.config)
		if strings.Contains(masked, tc.secret) || !strings.Contains(masked, tc.kept) || !strings.Contains(masked, netcfg.MaskPrefix) {
			t.Errorf("%q:\n%s", tc.config, masked)
		}
	}
	for _, kept := range []string{
		"password encryption aes",
		"snmp-server user v1user g1 v2c access 10",
		"ip ssh server algorithm authentication password",
		"interface Tunnel100\n tunnel key 100",
	} {
		if got := netcfg.Mask(kept); got != kept {
			t.Errorf("%q was masked:\n%s", kept, got)
		}
	}
}

// TestMask_SessionKeysKeepTheirSPIAndKeywords: a manual IPsec session key
// line comes back with only its keys replaced, each by its own placeholder,
// so the direction, protocol, SPI and the cipher and authenticator keywords
// still say which key is which, and every key is learned for the run's
// masking set. A line that names a session key without one (the no form)
// and the entry's other lines stay as they are.
func TestMask_SessionKeysKeepTheirSPIAndKeywords(t *testing.T) {
	const cipher, auth = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210fedcba98"
	config := "crypto map m 10 ipsec-manual\n set peer 10.0.0.5\n set session-key inbound esp 300 cipher " + cipher + " authenticator " + auth + "\n no set session-key outbound esp"
	masked, found, err := netcfg.MaskChecked(config)
	if err != nil {
		t.Fatalf("MaskChecked: %v", err)
	}
	want := "crypto map m 10 ipsec-manual\n set peer 10.0.0.5\n set session-key inbound esp 300 cipher " + netcfg.Placeholder(cipher) + " authenticator " + netcfg.Placeholder(auth) + "\n no set session-key outbound esp"
	if masked != want {
		t.Errorf("masked:\n%s\nwant:\n%s", masked, want)
	}
	if strings.Join(found, "|") != auth+"|"+cipher {
		t.Errorf("found %d secrets, want the authenticator and cipher keys", len(found))
	}
}

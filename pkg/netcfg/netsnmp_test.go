// Tests of the SNMP secrets of both configurations a device holds them in:
// IOS's snmp-server lines and a net-snmp agent's snmpd.conf.
package netcfg_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// TestMask_SNMPSecretsOfEveryVersion holds every line shape that carries an
// SNMP community or an SNMPv3 passphrase, on IOS and in snmpd.conf, to
// masking each secret, learning it for the run's masking set, keeping the
// rest of the line, staying put when masked again, and passing MaskChecked.
// A short community is masked here like a long one: this shape is the only
// masking such a community gets.
func TestMask_SNMPSecretsOfEveryVersion(t *testing.T) {
	for _, tc := range []struct {
		config string
		secret []string
		kept   string
	}{
		// IOS: a community, a short one included, and a v3 user at each
		// security level and with every algorithm form IOS XE prints.
		{"snmp-server community public RO", []string{"public"}, "snmp-server community "},
		{"snmp-server community c0mmunity-s3cret RW 99", []string{"c0mmunity-s3cret"}, " RW 99"},
		{"snmp-server user monitor grp v3 auth sha auth-pass-1 priv aes 256 priv-pass-1", []string{"auth-pass-1", "priv-pass-1"}, "snmp-server user monitor grp v3 auth sha "},
		{"snmp-server user monitor grp v3 auth sha-2 512 auth-pass-2 priv 3des priv-pass-2 access 10", []string{"auth-pass-2", "priv-pass-2"}, " access 10"},
		{"snmp-server user monitor grp v3 auth md5 auth-pass-3", []string{"auth-pass-3"}, "snmp-server user monitor grp v3 auth md5 "},
		// net-snmp: each community line, an option before the name, a short
		// community, and createUser in every form snmpd.conf(5) gives.
		{"rocommunity public 10.0.0.0/24", []string{"public"}, " 10.0.0.0/24"},
		{"rwcommunity c0mmunity-s3cret default -V all", []string{"c0mmunity-s3cret"}, " default -V all"},
		{"rocommunity6 v6-community-1 fd00::/8", []string{"v6-community-1"}, " fd00::/8"},
		{"rwcommunity6 v6-community-2", []string{"v6-community-2"}, "rwcommunity6 "},
		{"com2sec local localhost com2sec-secret-1", []string{"com2sec-secret-1"}, "com2sec local localhost "},
		{"com2sec -Cn ctx mynet 10.0.0.0/8 com2sec-secret-2", []string{"com2sec-secret-2"}, "com2sec -Cn ctx mynet 10.0.0.0/8 "},
		{"createUser monitor SHA auth-pass-4 AES priv-pass-4", []string{"auth-pass-4", "priv-pass-4"}, "createUser monitor SHA "},
		{"createUser -e 0x8000000001020304 monitor SHA-256 auth-pass-5 AES-256 priv-pass-5", []string{"auth-pass-5", "priv-pass-5"}, "createUser -e 0x8000000001020304 monitor SHA-256 "},
		{`createUser monitor MD5 "auth pass six" DES "priv pass six"`, []string{"auth pass six", "priv pass six"}, `createUser monitor MD5 "`},
		{"createUser monitor SHA-512 auth-pass-7 AES", []string{"auth-pass-7"}, " AES"},
		{"createUser monitor sha auth-pass-8", []string{"auth-pass-8"}, "createUser monitor sha "},
	} {
		masked, found := netcfg.MaskFound(tc.config)
		for _, s := range tc.secret {
			for _, part := range strings.Fields(s) {
				if strings.Contains(masked, part) {
					t.Errorf("%q kept %q:\n%s", tc.config, part, masked)
				}
			}
			learned := false
			for _, f := range found {
				learned = learned || f == s
			}
			if !learned {
				t.Errorf("%q: found %q, want %q among them", tc.config, found, s)
			}
		}
		if !strings.Contains(masked, tc.kept) || !strings.Contains(masked, netcfg.MaskPrefix) {
			t.Errorf("%q lost %q:\n%s", tc.config, tc.kept, masked)
		}
		if again := netcfg.Mask(masked); again != masked {
			t.Errorf("%q: masking a masked line changed it:\n%s", tc.config, again)
		}
		if checked, _, err := netcfg.MaskChecked(tc.config); err != nil || checked != masked {
			t.Errorf("%q: MaskChecked = %v, or disagrees with Mask:\n%s", tc.config, err, checked)
		}
	}
}

// TestMask_SNMPLinesWithNoSecretStayAsTheyAre: a createUser line naming no
// algorithm, a community line with no community, and an IOS v3 group or a
// v2c user hold no secret, and stay byte for byte.
func TestMask_SNMPLinesWithNoSecretStayAsTheyAre(t *testing.T) {
	for _, kept := range []string{
		"rocommunity",
		"createUser monitor",
		"createUser monitor usm",
		"snmp-server group grp v3 priv read all",
		"snmp-server user v2user grp v2c access 10",
		"syslocation the republic of public works",
	} {
		if got := netcfg.Mask(kept); got != kept {
			t.Errorf("%q was masked:\n%s", kept, got)
		}
	}
}

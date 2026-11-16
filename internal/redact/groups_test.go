// Tests of the SNMP shapes in the ruleset and of the secret_groups form they
// use: each secret masked, its keyword kept, a placeholder left alone, and a
// rule that could mask nothing refused.
package redact_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// TestSNMPShapes_MaskEachSecretAndKeepItsKeyword runs every SNMP line shape
// through the shared masker with no value known, the way a short community
// is masked in every run: each community and passphrase goes, the keyword
// and the rest of the line stay, a placeholder pkg/netcfg wrote is left as
// it is, and an ordinary word holding a short community is untouched.
func TestSNMPShapes_MaskEachSecretAndKeepItsKeyword(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"snmp-server community public RO", "snmp-server community ******** RO"},
		{"snmp-server   community  public RW 99", "snmp-server   community  ******** RW 99"},
		{"rocommunity public 10.0.0.0/24", "rocommunity ******** 10.0.0.0/24"},
		{"RWCOMMUNITY6 c0mmunity-s3cret", "RWCOMMUNITY6 ********"},
		{"com2sec -Cn ctx mynet 10.0.0.0/8 public", "com2sec -Cn ctx mynet 10.0.0.0/8 ********"},
		{"snmp-server user monitor grp v3 auth sha-2 256 auth-pass-1 priv aes 128 priv-pass-1 access 10",
			"snmp-server user monitor grp v3 auth sha-2 256 ******** priv aes 128 ******** access 10"},
		{"snmp-server user monitor grp v3 auth md5 auth-pass-2", "snmp-server user monitor grp v3 auth md5 ********"},
		{`createUser -e 0x80001f88 monitor SHA-512 "auth pass three" AES-192 priv-pass-3`, "createUser -e 0x80001f88 monitor SHA-512 ******** AES-192 ********"},
		{"createUser monitor SHA auth-pass-4 AES", "createUser monitor SHA ******** AES"},
		// NX-OS's own spellings of a v3 user's algorithms, and its priv with
		// no algorithm (DES), which prints localized keys.
		{"snmp-server user admin network-admin auth sha-256 auth-pass-9 priv aes-128 priv-pass-9",
			"snmp-server user admin network-admin auth sha-256 ******** priv aes-128 ********"},
		{"snmp-server user admin network-admin auth md5 0x1a2b3c4d priv 0x5e6f7a8b localizedkey",
			"snmp-server user admin network-admin auth md5 ******** priv ******** localizedkey"},
		// A notification host's community, after any vrf, traps or informs
		// and version; a v3 host's word is a user, and NX-OS's host lines
		// naming a vrf or an interface hold no community.
		{"snmp-server host 10.0.0.1 version 2c public", "snmp-server host 10.0.0.1 version 2c ********"},
		{"snmp-server host 192.0.2.1 vrf Mgmt-intf informs version 2c public udp-port 162", "snmp-server host 192.0.2.1 vrf Mgmt-intf informs version 2c ******** udp-port 162"},
		{"snmp-server host 10.0.0.1 traps version 1 public", "snmp-server host 10.0.0.1 traps version 1 ********"},
		{"snmp-server host 10.0.0.1 public", "snmp-server host 10.0.0.1 ********"},
		{"snmp-server host 10.0.0.1 informs version 3 priv monitor", "snmp-server host 10.0.0.1 informs version 3 priv monitor"},
		{"snmp-server host 10.0.0.1 use-vrf management", "snmp-server host 10.0.0.1 use-vrf management"},
		{"snmp-server host 10.0.0.1 source-interface mgmt0", "snmp-server host 10.0.0.1 source-interface mgmt0"},
		// A net-snmp agent's trap sinks and trap community, and snmptrapd's
		// accepted community.
		{"trapsink 10.0.0.1 public", "trapsink 10.0.0.1 ********"},
		{"trap2sink nms.example.com:162 public 162", "trap2sink nms.example.com:162 ******** 162"},
		{"informsink nms.example.com public", "informsink nms.example.com ********"},
		{"trapsink 10.0.0.1", "trapsink 10.0.0.1"},
		{"trapcommunity public", "trapcommunity ********"},
		{"authCommunity log,execute,net public", "authCommunity log,execute,net ********"},
		// trapsess's options, apart or attached, in either order; the
		// protocol options (-a, -x) are not secrets.
		{"trapsess -v 2c -c public 10.0.0.1", "trapsess -v 2c -c ******** 10.0.0.1"},
		{"trapsess -v2c -cpublic nms.example.com", "trapsess -v2c -c******** nms.example.com"},
		{"trapsess -v 3 -u monitor -l authPriv -a SHA -A auth-pass-7 -x AES -X priv-pass-7 10.0.0.1",
			"trapsess -v 3 -u monitor -l authPriv -a SHA -A ******** -x AES -X ******** 10.0.0.1"},
		{"trapsess -v 3 -u monitor -l authPriv -X priv-pass-8 -a SHA -A auth-pass-8 10.0.0.1",
			"trapsess -v 3 -u monitor -l authPriv -X ******** -a SHA -A ******** 10.0.0.1"},
		// Masked already: a pkg/netcfg placeholder keeps the comparison it
		// makes possible, and this package's own is not masked again.
		{"snmp-server community pleiades-masked-0123456789abcdef RO", "snmp-server community pleiades-masked-0123456789abcdef RO"},
		{"rocommunity ******** default", "rocommunity ******** default"},
		// No shape: a short community inside an ordinary word, and lines
		// that hold no secret.
		{"syslocation the republic of public works", "syslocation the republic of public works"},
		{"snmp-server group grp v3 priv read all", "snmp-server group grp v3 priv read all"},
		{"createUser monitor", "createUser monitor"},
	} {
		if got := redact.Text(nil, tc.line); got != tc.want {
			t.Errorf("Text(%q)\n got %q\nwant %q", tc.line, got, tc.want)
		}
	}
}

// TestSecretGroups_RefusedWhereTheyCouldMaskNothing: secret_groups on a key
// rule is refused by Validate, and on a pattern with no group by NewMasker,
// since either would be a rule that masks nothing and says so nowhere.
func TestSecretGroups_RefusedWhereTheyCouldMaskNothing(t *testing.T) {
	keyRule := redact.Ruleset{Rules: []redact.Rule{{Name: "k", Kind: redact.KindKey, Keys: []string{"x"}, SecretGroups: true, Description: "d"}}}
	if err := keyRule.Validate(); err == nil || !strings.Contains(err.Error(), "secret_groups") {
		t.Errorf("Validate(secret_groups on a key rule) = %v", err)
	}
	noGroup := redact.Ruleset{Rules: []redact.Rule{{Name: "p", Kind: redact.KindPattern, Pattern: `secret-[a-z]+`, SecretGroups: true, Samples: []string{"secret-x"}, Description: "d"}}}
	if _, err := redact.NewMasker(noGroup); err == nil || !strings.Contains(err.Error(), "no group") {
		t.Errorf("NewMasker(secret_groups with no group) = %v", err)
	}
	nested := redact.Ruleset{Rules: []redact.Rule{{Name: "n", Kind: redact.KindPattern, Pattern: `key=((a)b+)`, SecretGroups: true, Samples: []string{"key=abb"}, Description: "d"}}}
	m, err := redact.NewMasker(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Text(nil, "key=abbb and key=ab"); got != "key=******** and key=********" {
		t.Errorf("a nested group was masked twice or not at all: %q", got)
	}
}

// TestSNMPKeyNames_MaskTheirValuesInALog: a structured log attribute named
// for an SNMP secret, by its flattened key or a passphrase's pleiades_snmp
// input id, is masked whatever its value, a short community included, and
// the USM user and protocol names, which are not secrets, are not. A bare
// "community" names no secret: BGP communities are not secrets, and a name
// matches by any run of its words, so a parsed BGP summary's
// community_entries would be refused as a fact.
func TestSNMPKeyNames_MaskTheirValuesInALog(t *testing.T) {
	logger, buf, m := newTestLogger(t)
	for _, name := range []string{"community", "community_entries", "bgp_community"} {
		if m.SecretName(name) {
			t.Errorf("SecretName(%q) = true, and BGP communities are not secrets", name)
		}
	}
	secrets := map[string]string{
		"snmp_community": "pub2", "snmp_auth_passphrase": "auth-pass-phrase-3",
		"snmp_priv_passphrase": "priv-pass-phrase-4", "auth_passphrase": "auth-pass-phrase-5", "priv_passphrase": "priv-pass-phrase-6",
	}
	var args []any
	for key, value := range secrets {
		args = append(args, key, value)
		if !m.SecretName(key) {
			t.Errorf("SecretName(%q) = false", key)
		}
	}
	logger.Info("storing an SNMP credential", append(args, "snmp_user", "monitor", "snmp_auth_protocol", "sha256")...)
	for key, value := range secrets {
		if strings.Contains(buf.String(), value) {
			t.Errorf("the %s attribute's value reached the log: %s", key, buf.String())
		}
	}
	for _, shown := range []string{`"snmp_user":"monitor"`, `"snmp_auth_protocol":"sha256"`} {
		if !strings.Contains(buf.String(), shown) {
			t.Errorf("an identifier was masked, %s missing from %s", shown, buf.String())
		}
	}
}

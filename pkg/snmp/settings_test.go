// Tests for a device's SNMP settings: what a record may say, what each
// weakening needs, and what each makes a run say.
package snmp_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// parse reads raw as a record's properties.
func parse(raw map[string]inventory.PropertyValue) (snmp.Settings, error) {
	return snmp.Parse(inventory.NewProperties(raw))
}

// TestParse_EveryWeakeningNeedsItsOwnFlag: v3 is the default; v1 and v2c
// need the cleartext flag, which v3 refuses; and every bound is enforced.
func TestParse_EveryWeakeningNeedsItsOwnFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]inventory.PropertyValue
		ok   bool
	}{
		{"nothing set", nil, true},
		{"v3 as a number", map[string]inventory.PropertyValue{"snmp_version": 3}, true},
		{"v3 from JSON", map[string]inventory.PropertyValue{"snmp_version": float64(3)}, true},
		{"v2c without the flag", map[string]inventory.PropertyValue{"snmp_version": "2c"}, false},
		{"v2c with the flag", map[string]inventory.PropertyValue{"snmp_version": "2c", "snmp_allow_cleartext_community": true}, true},
		{"v1 with the flag", map[string]inventory.PropertyValue{"snmp_version": 1, "snmp_allow_cleartext_community": true}, true},
		{"the flag with v3", map[string]inventory.PropertyValue{"snmp_allow_cleartext_community": true}, false},
		{"the flag not a boolean", map[string]inventory.PropertyValue{"snmp_version": "2c", "snmp_allow_cleartext_community": "yes"}, false},
		{"the legacy flag with v2c", map[string]inventory.PropertyValue{"snmp_version": "2c", "snmp_allow_cleartext_community": true, "snmp_allow_legacy_algorithms": true}, false},
		{"the legacy flag with v3", map[string]inventory.PropertyValue{"snmp_allow_legacy_algorithms": true}, true},
		{"an unknown version", map[string]inventory.PropertyValue{"snmp_version": "2"}, false},
		{"a fractional version", map[string]inventory.PropertyValue{"snmp_version": 2.5}, false},
		{"port 0", map[string]inventory.PropertyValue{"snmp_port": 0}, false},
		{"port 1161", map[string]inventory.PropertyValue{"snmp_port": 1161}, true},
		{"a port as text", map[string]inventory.PropertyValue{"snmp_port": "161"}, false},
		{"a fractional port", map[string]inventory.PropertyValue{"snmp_port": 161.5}, false},
		{"timeout 0", map[string]inventory.PropertyValue{"snmp_timeout": 0}, false},
		{"timeout past the bound", map[string]inventory.PropertyValue{"snmp_timeout": 121}, false},
		{"retries past the bound", map[string]inventory.PropertyValue{"snmp_retries": 11}, false},
		{"no bulk on v1", map[string]inventory.PropertyValue{"snmp_version": 1, "snmp_allow_cleartext_community": true, "snmp_no_bulk": true}, false},
		{"repetitions with no bulk", map[string]inventory.PropertyValue{"snmp_no_bulk": true, "snmp_max_repetitions": 5}, false},
		{"repetitions on v1", map[string]inventory.PropertyValue{"snmp_version": 1, "snmp_allow_cleartext_community": true, "snmp_max_repetitions": 5}, false},
		{"repetitions past the bound", map[string]inventory.PropertyValue{"snmp_max_repetitions": 101}, false},
		{"an interval past the bound", map[string]inventory.PropertyValue{"snmp_min_interval_ms": 10001}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parse(tc.raw); (err == nil) != tc.ok {
				t.Fatalf("Parse = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	s, err := parse(map[string]inventory.PropertyValue{"snmp_port": 1161, "snmp_timeout": 9, "snmp_retries": 0, "snmp_max_repetitions": 25, "snmp_min_interval_ms": 250})
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 1161 || s.Version != snmp.Version3 || s.Timeout != 9*time.Second || s.Retries != 0 || s.MaxRepetitions != 25 || s.MinInterval != 250*time.Millisecond {
		t.Errorf("Parse read %+v", s)
	}
	if got := snmp.For(struct{}{}); got != snmp.Defaults() {
		t.Errorf("For a device with no settings = %+v, want the defaults", got)
	}
}

// v3 builds a v3 user at the level the protocols give.
func v3(auth wire.SNMPAuthProtocol, priv wire.SNMPPrivProtocol) wire.SNMPCredential {
	c := wire.SNMPCredential{User: "ops"}
	if auth != "" {
		c.AuthProtocol, c.AuthPassphrase = auth, "authpassword"
	}
	if priv != "" {
		c.PrivProtocol, c.PrivPassphrase = priv, "privpassword"
	}
	return c
}

// TestCheck_TheCredentialMustFitTheDevice: a community for v1/v2c, a user for
// v3, the legacy flag exactly when a legacy algorithm is used, and never 3DES.
func TestCheck_TheCredentialMustFitTheDevice(t *testing.T) {
	v2c := snmp.Settings{Version: snmp.Version2c, AllowCleartextCommunity: true}
	cur := snmp.Defaults()
	legacy := snmp.Defaults()
	legacy.AllowLegacyAlgorithms = true
	for _, tc := range []struct {
		name string
		s    snmp.Settings
		cred wire.SNMPCredential
		ok   bool
	}{
		{"a community for v2c", v2c, wire.SNMPCredential{Community: "community-1"}, true},
		{"a user for v2c", v2c, v3(wire.SNMPAuthSHA, ""), false},
		{"nothing for v2c", v2c, wire.SNMPCredential{}, false},
		{"a community for v3", cur, wire.SNMPCredential{Community: "community-1"}, false},
		{"nothing for v3", cur, wire.SNMPCredential{}, false},
		{"noAuthNoPriv", cur, v3("", ""), true},
		{"sha256 and aes256-reeder", cur, v3(wire.SNMPAuthSHA256, wire.SNMPPrivAES256Reeder), true},
		{"md5 without the flag", cur, v3(wire.SNMPAuthMD5, ""), false},
		{"des without the flag", cur, v3(wire.SNMPAuthSHA, wire.SNMPPrivDES), false},
		{"md5 and des with the flag", legacy, v3(wire.SNMPAuthMD5, wire.SNMPPrivDES), true},
		{"the flag with nothing legacy", legacy, v3(wire.SNMPAuthSHA, wire.SNMPPrivAES128), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.s.Check(tc.cred); (err == nil) != tc.ok {
				t.Fatalf("Check = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	if err := legacy.Check(v3(wire.SNMPAuthSHA, wire.SNMPPriv3DES)); !errors.Is(err, snmp.ErrUnsupportedPrivacy) {
		t.Errorf("Check with 3des = %v, want ErrUnsupportedPrivacy", err)
	}
}

// TestWarnings_SayWhatEachWeakeningMeans: a community, a legacy algorithm
// and each level below authPriv are warned about, by the device's name, and
// an authPriv user with current algorithms is not.
func TestWarnings_SayWhatEachWeakeningMeans(t *testing.T) {
	v2c := snmp.Settings{Version: snmp.Version2c, AllowCleartextCommunity: true}
	legacy := snmp.Defaults()
	legacy.AllowLegacyAlgorithms = true
	for _, tc := range []struct {
		name string
		s    snmp.Settings
		cred wire.SNMPCredential
		want []string
	}{
		{"authPriv", snmp.Defaults(), v3(wire.SNMPAuthSHA512, wire.SNMPPrivAES256), nil},
		{"v2c", v2c, wire.SNMPCredential{Community: "community-1"}, []string{"SNMPv2c", "in the clear", "snmp_allow_cleartext_community"}},
		{"legacy", legacy, v3(wire.SNMPAuthMD5, wire.SNMPPrivDES), []string{"md5 authentication and des privacy", "snmp_allow_legacy_algorithms"}},
		{"authNoPriv", snmp.Defaults(), v3(wire.SNMPAuthSHA, ""), []string{"authNoPriv", "unencrypted"}},
		{"noAuthNoPriv", snmp.Defaults(), v3("", ""), []string{"noAuthNoPriv", "nothing authenticates"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(tc.s.Warnings("old-switch", tc.cred), "\n")
			if len(tc.want) == 0 && got != "" {
				t.Fatalf("Warnings = %q, want none", got)
			}
			for _, w := range append(tc.want, `"old-switch"`) {
				if len(tc.want) > 0 && !strings.Contains(got, w) {
					t.Errorf("Warnings = %q, want it to say %q", got, w)
				}
			}
			if strings.Contains(got, "authpassword") || strings.Contains(got, "community-1") {
				t.Errorf("Warnings hold a secret: %q", got)
			}
		})
	}
	if snmp.Authenticated(wire.SNMPCredential{Community: "community-1"}) || snmp.Authenticated(v3("", "")) || !snmp.Authenticated(v3(wire.SNMPAuthSHA, "")) {
		t.Error("Authenticated does not follow the security level")
	}
}

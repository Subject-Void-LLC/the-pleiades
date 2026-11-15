// Tests of the SNMP credential's vocabulary, its derived security level, and
// the one check every write path holds it to.
package wire_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSNMPCredential_CheckAcceptsEveryVersionAndEveryProtocol: a community,
// a user at each security level, and every authentication and privacy
// protocol in the vocabulary, the old ones and both AES-192/256 key
// extensions included, are stored.
func TestSNMPCredential_CheckAcceptsEveryVersionAndEveryProtocol(t *testing.T) {
	accepted := []wire.SNMPCredential{
		{Community: "a-long-community"},
		{User: "monitor"},
		{User: "monitor", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: "auth-pass-1"},
	}
	for _, auth := range wire.SNMPAuthProtocols() {
		for _, priv := range wire.SNMPPrivProtocols() {
			accepted = append(accepted, wire.SNMPCredential{User: "u", AuthProtocol: auth, AuthPassphrase: "auth-pass-1", PrivProtocol: priv, PrivPassphrase: "priv-pass-1"})
		}
	}
	for _, c := range accepted {
		if err := c.Check(false); err != nil {
			t.Errorf("Check(%v) = %v, want it accepted", c, err)
		}
	}
	if got := len(wire.SNMPAuthProtocols()) * len(wire.SNMPPrivProtocols()); got != 6*7 {
		t.Errorf("the vocabulary has %d combinations, want 6 auth by 7 priv", got)
	}
	for _, name := range []wire.SNMPPrivProtocol{"aes192", "aes256", "aes192-reeder", "aes256-reeder"} {
		if !name.Valid() {
			t.Errorf("%q is not in the privacy vocabulary, want both AES key extensions named apart", name)
		}
	}
}

// TestSNMPCredential_CheckRefusesWhatNoAgentAccepts holds each rule to the
// key it names, and to never repeating a value in its message.
func TestSNMPCredential_CheckRefusesWhatNoAgentAccepts(t *testing.T) {
	const auth, priv = "auth-pass-1", "priv-pass-1"
	for _, tc := range []struct {
		name  string
		cred  wire.SNMPCredential
		allow bool
		key   string
	}{
		{"community with a user", wire.SNMPCredential{Community: "a-long-community", User: "u"}, false, wire.SecretSNMPCommunity},
		{"community with a protocol", wire.SNMPCredential{Community: "a-long-community", AuthProtocol: wire.SNMPAuthSHA}, false, wire.SecretSNMPCommunity},
		{"short community", wire.SNMPCredential{Community: "public"}, false, wire.SecretSNMPCommunity},
		{"allowance with a long community", wire.SNMPCredential{Community: "a-long-community"}, true, wire.SecretSNMPCommunity},
		{"allowance with no community", wire.SNMPCredential{User: "u"}, true, wire.SecretSNMPCommunity},
		{"protocol without a user", wire.SNMPCredential{AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: auth}, false, wire.SecretSNMPUser},
		{"passphrase without a user", wire.SNMPCredential{PrivPassphrase: priv}, false, wire.SecretSNMPUser},
		{"user too long", wire.SNMPCredential{User: strings.Repeat("u", 33)}, false, wire.SecretSNMPUser},
		{"user with a line break", wire.SNMPCredential{User: "u\nforged line"}, false, wire.SecretSNMPUser},
		{"unknown auth protocol", wire.SNMPCredential{User: "u", AuthProtocol: "sha1", AuthPassphrase: auth}, false, wire.SecretSNMPAuthProtocol},
		{"upper-case auth protocol", wire.SNMPCredential{User: "u", AuthProtocol: "SHA", AuthPassphrase: auth}, false, wire.SecretSNMPAuthProtocol},
		{"auth protocol without passphrase", wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA}, false, wire.SecretSNMPAuthPassphrase},
		{"auth passphrase without protocol", wire.SNMPCredential{User: "u", AuthPassphrase: auth}, false, wire.SecretSNMPAuthProtocol},
		{"short auth passphrase", wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthMD5, AuthPassphrase: "seven77"}, false, wire.SecretSNMPAuthPassphrase},
		{"unknown priv protocol", wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: auth, PrivProtocol: "aes", PrivPassphrase: priv}, false, wire.SecretSNMPPrivProtocol},
		{"priv protocol without passphrase", wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: auth, PrivProtocol: wire.SNMPPrivAES128}, false, wire.SecretSNMPPrivPassphrase},
		{"priv passphrase without protocol", wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: auth, PrivPassphrase: priv}, false, wire.SecretSNMPPrivProtocol},
		{"short priv passphrase", wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: auth, PrivProtocol: wire.SNMPPrivDES, PrivPassphrase: "q7x2"}, false, wire.SecretSNMPPrivPassphrase},
		{"privacy without authentication", wire.SNMPCredential{User: "u", PrivProtocol: wire.SNMPPrivAES256, PrivPassphrase: priv}, false, wire.SecretSNMPPrivProtocol},
	} {
		err := tc.cred.Check(tc.allow)
		var problem *wire.SNMPProblem
		if !errors.As(err, &problem) {
			t.Errorf("%s: Check = %v, want an *SNMPProblem", tc.name, err)
			continue
		}
		if problem.Key != tc.key {
			t.Errorf("%s: the problem names %q, want %q (%v)", tc.name, problem.Key, tc.key, err)
		}
		for _, secret := range []string{tc.cred.Community, tc.cred.AuthPassphrase, tc.cred.PrivPassphrase} {
			if secret != "" && strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the refusal %q repeats a secret", tc.name, err)
			}
		}
	}
}

// TestSNMPCredential_AShortCommunityIsStoredOnlyWhenAllowed: the allowance
// is what stores "public", and ShortCommunity says it is short.
func TestSNMPCredential_AShortCommunityIsStoredOnlyWhenAllowed(t *testing.T) {
	short := wire.SNMPCredential{Community: "public"}
	if err := short.Check(true); err != nil {
		t.Errorf("Check(true) = %v, want an allowed short community stored", err)
	}
	if !short.ShortCommunity() {
		t.Error("ShortCommunity() = false for a six-character community")
	}
	if (wire.SNMPCredential{Community: "a-long-community"}).ShortCommunity() || (wire.SNMPCredential{}).ShortCommunity() {
		t.Error("ShortCommunity() = true for a long community or none")
	}
}

// TestSNMPCredential_LevelIsDerivedFromWhatIsSet: noAuthNoPriv, authNoPriv
// and authPriv follow from the protocols present, and a community has none.
func TestSNMPCredential_LevelIsDerivedFromWhatIsSet(t *testing.T) {
	for _, tc := range []struct {
		cred wire.SNMPCredential
		want wire.SNMPSecurityLevel
	}{
		{wire.SNMPCredential{Community: "a-long-community"}, ""},
		{wire.SNMPCredential{User: "u"}, wire.SNMPNoAuthNoPriv},
		{wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA256, AuthPassphrase: "auth-pass-1"}, wire.SNMPAuthNoPriv},
		{wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA256, AuthPassphrase: "auth-pass-1", PrivProtocol: wire.SNMPPrivAES128, PrivPassphrase: "priv-pass-1"}, wire.SNMPAuthPriv},
	} {
		if got := tc.cred.Level(); got != tc.want {
			t.Errorf("Level(%v) = %q, want %q", tc.cred, got, tc.want)
		}
	}
}

// TestSNMPCredential_SecretsRoundTripThroughTheWireKeys: SNMPOf reads back
// what Secrets wrote, an unset field absent from the map, and SNMPKeys names
// every key Secrets can write.
func TestSNMPCredential_SecretsRoundTripThroughTheWireKeys(t *testing.T) {
	full := wire.SNMPCredential{User: "u", AuthProtocol: wire.SNMPAuthSHA512, AuthPassphrase: "auth-pass-1", PrivProtocol: wire.SNMPPrivAES256Reeder, PrivPassphrase: "priv-pass-1"}
	secrets := full.Secrets()
	if got := wire.SNMPOf(secrets); got != full {
		t.Errorf("SNMPOf(Secrets()) = %v, want %v", got, full)
	}
	community := wire.SNMPCredential{Community: "a-long-community"}.Secrets()
	if len(community) != 1 || community[wire.SecretSNMPCommunity] != "a-long-community" {
		t.Errorf("a community alone flattened to %d keys, want only %s", len(community), wire.SecretSNMPCommunity)
	}
	keys := map[string]bool{}
	for _, k := range wire.SNMPKeys() {
		keys[k] = true
	}
	for k := range secrets {
		if !keys[k] {
			t.Errorf("Secrets() wrote %q, which SNMPKeys does not name", k)
		}
	}
	if len(keys) != 6 || !keys[wire.SecretSNMPCommunity] {
		t.Errorf("SNMPKeys() = %v, want the six SNMP keys", wire.SNMPKeys())
	}
	if !(wire.SNMPCredential{}).Empty() || full.Empty() {
		t.Error("Empty() is wrong for the zero value or a full user")
	}
}

// TestSNMPCredential_PrintsNoSecret: every print path a struct reaches (%v,
// %+v, %#v, %s, encoding/json) shows the user and protocols and marks the
// secrets, never their values.
func TestSNMPCredential_PrintsNoSecret(t *testing.T) {
	user := wire.SNMPCredential{User: "monitor", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: "auth-secret-1", PrivProtocol: wire.SNMPPrivAES192Reeder, PrivPassphrase: "priv-secret-1"}
	community := wire.SNMPCredential{Community: "community-secret-1"}
	js, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	cjs, _ := json.Marshal(community)
	outputs := []string{fmt.Sprintf("%v", user), fmt.Sprintf("%+v", user), fmt.Sprintf("%#v", user), fmt.Sprintf("%s", user), string(js), fmt.Sprintf("%v", community), string(cjs)}
	for _, out := range outputs {
		for _, secret := range []string{"auth-secret-1", "priv-secret-1", "community-secret-1"} {
			if strings.Contains(out, secret) {
				t.Errorf("a print path leaked %q: %s", secret, out)
			}
		}
	}
	if !strings.Contains(outputs[0], "monitor") || !strings.Contains(outputs[0], "aes192-reeder") || !strings.Contains(outputs[0], "<redacted, set>") {
		t.Errorf("String() = %s, want the user, the protocol and a set marker", outputs[0])
	}
}

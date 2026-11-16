// Tests for MaskedValues, the one rule deciding what of a credential every
// output masks by value.
package wire

import (
	"slices"
	"testing"
)

// TestMaskedValues_MasksEverythingButIdentifiers: the password, key and
// passphrase are masked, and so is a key the rule has never heard of, while
// the username, a certificate's public half and a seeded login's
// identifiers stay readable, since a substring scrub of a username like root
// would corrupt every path under /root in a report.
func TestMaskedValues_MasksEverythingButIdentifiers(t *testing.T) {
	got := MaskedValues(map[string]string{
		SecretUsername:          "root",
		SecretCertificatePEM:    "-----BEGIN CERTIFICATE-----",
		SecretSeedUsername:      "seeded",
		SecretSeedAuthorizedKey: "ssh-ed25519 AAAA seeded",
		SecretSeedHost:          "10.0.0.7",
		SecretPassword:          "hunter2-long-enough",
		SecretPrivateKeyPEM:     "-----BEGIN OPENSSH PRIVATE KEY-----",
		SecretPassphrase:        "a-key-passphrase",
		"api_token_added_later": "a-future-secret",
	})
	slices.Sort(got)
	want := []string{"-----BEGIN OPENSSH PRIVATE KEY-----", "a-future-secret", "a-key-passphrase", "hunter2-long-enough"}
	if !slices.Equal(got, want) {
		t.Errorf("MaskedValues() = %q, want %q", got, want)
	}
	if len(MaskedValues(nil)) != 0 {
		t.Error("a device with no credential masked something")
	}
}

// TestMaskedValues_MasksSNMPSecretsAndNotItsNames: a community and both USM
// passphrases are masked; the USM user and the protocol names are not, since
// masking "aes256-reeder" by value would scrub that word from every output;
// and a community too short to mask by value is left out, to be masked by
// shape alone.
func TestMaskedValues_MasksSNMPSecretsAndNotItsNames(t *testing.T) {
	got := MaskedValues(map[string]string{
		SecretUsername:           "admin",
		SecretSNMPUser:           "snmp",
		SecretSNMPAuthProtocol:   string(SNMPAuthSHA512),
		SecretSNMPAuthPassphrase: "auth-pass-phrase",
		SecretSNMPPrivProtocol:   string(SNMPPrivAES256Reeder),
		SecretSNMPPrivPassphrase: "priv-pass-phrase",
	})
	slices.Sort(got)
	if want := []string{"auth-pass-phrase", "priv-pass-phrase"}; !slices.Equal(got, want) {
		t.Errorf("MaskedValues(v3 user) = %q, want only the passphrases %q", got, want)
	}
	if got := MaskedValues(map[string]string{SecretSNMPCommunity: "c0mmunity-s3cret"}); !slices.Equal(got, []string{"c0mmunity-s3cret"}) {
		t.Errorf("MaskedValues(long community) = %q, want it masked", got)
	}
	if got := MaskedValues(map[string]string{SecretSNMPCommunity: "public", SecretPassword: "hunter2-long-enough"}); !slices.Equal(got, []string{"hunter2-long-enough"}) {
		t.Errorf("MaskedValues(short community) = %q, want the community left out and the password kept", got)
	}
}

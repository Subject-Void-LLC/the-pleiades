// Tests for credentialSecrets, which decides what of a method's credential
// the run masks (Phase 117a, M1).
package engine

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestCredentialSecrets_MasksEverythingButIdentifiers: the password, key
// and passphrase are masked, and so is a key the function has never heard
// of, while the username and a certificate's public half stay readable,
// since a substring scrub of a username like root would corrupt every path
// under /root in a report.
func TestCredentialSecrets_MasksEverythingButIdentifiers(t *testing.T) {
	got := credentialSecrets(map[string]string{
		wire.SecretUsername:       "root",
		wire.SecretCertificatePEM: "-----BEGIN CERTIFICATE-----",
		wire.SecretPassword:       "hunter2-long-enough",
		wire.SecretPrivateKeyPEM:  "-----BEGIN OPENSSH PRIVATE KEY-----",
		wire.SecretPassphrase:     "a-key-passphrase",
		"api_token_added_later":   "a-future-secret",
	})
	slices.Sort(got)
	want := []string{"-----BEGIN OPENSSH PRIVATE KEY-----", "a-future-secret", "a-key-passphrase", "hunter2-long-enough"}
	if !slices.Equal(got, want) {
		t.Errorf("credentialSecrets() = %q, want %q", got, want)
	}
	if len(credentialSecrets(nil)) != 0 {
		t.Error("a device with no credential masked something")
	}
}

// TestHopSecrets_MaskWhatATargetMasks: a bastion's every secret joins the
// masking set by the target's rule, its enable secret and token included,
// and its username does not. It masked a hop's password, key and passphrase
// alone before.
func TestHopSecrets_MaskWhatATargetMasks(t *testing.T) {
	got := hopSecrets([]transport.Hop{{Credential: credential.Credential{
		Username: "jump", Password: "jump-password-1", EnablePassword: "jump-enable-1", Token: "jump-token-12",
	}}})
	slices.Sort(got)
	if want := []string{"jump-enable-1", "jump-password-1", "jump-token-12"}; !slices.Equal(got, want) {
		t.Errorf("hopSecrets() = %q, want %q", got, want)
	}
}

// TestCredentialSecrets_MasksSNMPSecretsAndNotItsNames: a community and both
// USM passphrases are masked; the USM user and the protocol names are not,
// since masking "aes256-reeder" by value would scrub that word from every
// output; and a community too short to mask by value is left out, to be
// masked by shape alone.
func TestCredentialSecrets_MasksSNMPSecretsAndNotItsNames(t *testing.T) {
	got := CredentialSecrets(map[string]string{
		wire.SecretUsername:           "admin",
		wire.SecretSNMPUser:           "snmp",
		wire.SecretSNMPAuthProtocol:   string(wire.SNMPAuthSHA512),
		wire.SecretSNMPAuthPassphrase: "auth-pass-phrase",
		wire.SecretSNMPPrivProtocol:   string(wire.SNMPPrivAES256Reeder),
		wire.SecretSNMPPrivPassphrase: "priv-pass-phrase",
	})
	slices.Sort(got)
	if want := []string{"auth-pass-phrase", "priv-pass-phrase"}; !slices.Equal(got, want) {
		t.Errorf("CredentialSecrets(v3 user) = %q, want only the passphrases %q", got, want)
	}
	if got := CredentialSecrets(map[string]string{wire.SecretSNMPCommunity: "c0mmunity-s3cret"}); !slices.Equal(got, []string{"c0mmunity-s3cret"}) {
		t.Errorf("CredentialSecrets(long community) = %q, want it masked", got)
	}
	if got := CredentialSecrets(map[string]string{wire.SecretSNMPCommunity: "public", wire.SecretPassword: "hunter2-long-enough"}); !slices.Equal(got, []string{"hunter2-long-enough"}) {
		t.Errorf("CredentialSecrets(short community) = %q, want the community left out and the password kept", got)
	}
}

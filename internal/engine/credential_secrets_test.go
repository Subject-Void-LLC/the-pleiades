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

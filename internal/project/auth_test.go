// Package project_test's authentication coverage: the three shapes a forge
// actually offers, and the one place a secret could escape.
//
// A real encrypted SSH key is generated here rather than fixtured, because
// the interesting case is the passphrase and a checked-in key would either
// have its passphrase checked in beside it or not be encrypted at all.
package project_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// errFakeResolver stands in for a credential store that cannot answer.
var errFakeResolver = errors.New("the vault is sealed")

// ed25519Keys generates one key pair.
func ed25519Keys() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// stubAuth hands back a fixed Auth and records that it was asked.
type stubAuth struct {
	auth  project.Auth
	err   error
	calls []int
}

func (s *stubAuth) ResolveAuth(_ context.Context, id int) (project.Auth, error) {
	s.calls = append(s.calls, id)
	return s.auth, s.err
}

// newEncryptedKey returns a real ed25519 private key in PEM, encrypted with
// the given passphrase.
func newEncryptedKey(t *testing.T, passphrase string) []byte {
	t.Helper()
	_, priv, err := ed25519Keys()
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatalf("encrypting the key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

// newPlainKey returns a real unencrypted ed25519 private key in PEM.
func newPlainKey(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519Keys()
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshalling the key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// TestAuth_ShapesAreDistinguished covers the three forms a credential
// arrives in, which the transport has to tell apart before it can pick one.
func TestAuth_ShapesAreDistinguished(t *testing.T) {
	for _, tc := range []struct {
		name        string
		auth        project.Auth
		wantEmpty   bool
		wantUsesKey bool
	}{
		{"public repository", project.Auth{}, true, false},
		{"token alone", project.Auth{Password: "ghp-token"}, false, false},
		{"username and password", project.Auth{Username: "someone", Password: "hunter2"}, false, false},
		{"ssh key", project.Auth{PrivateKey: []byte("-----BEGIN-----")}, false, true},
		{"ssh key with a passphrase", project.Auth{PrivateKey: []byte("x"), Passphrase: "p"}, false, true},
		// A username with no secret of any kind is not a credential. It
		// must read as empty, or a public clone would be attempted with a
		// half-filled auth and fail for a confusing reason.
		{"username alone", project.Auth{Username: "someone"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.auth.Empty(); got != tc.wantEmpty {
				t.Errorf("Empty() = %v, want %v", got, tc.wantEmpty)
			}
			if got := tc.auth.UsesKey(); got != tc.wantUsesKey {
				t.Errorf("UsesKey() = %v, want %v", got, tc.wantUsesKey)
			}
		})
	}
}

// TestGitSyncer_ResolvesTheProjectsCredential proves the syncer asks for the
// credential the project names, rather than being handed one.
//
// That is the whole reason Sync takes no Auth: a caller that never holds a
// secret cannot leak the one it was given, which is what lets the UI and
// the API both trigger a private clone while internal/archtest still
// asserts neither can reach a plaintext value.
func TestGitSyncer_ResolvesTheProjectsCredential(t *testing.T) {
	origin, _ := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})
	auth := &stubAuth{}
	syncer := project.NewGitSyncer(t.TempDir(), auth)

	p := project.Project{
		ID: 1, OrganizationID: 1, SCMType: project.SCMGit,
		SCMURL: origin, CredentialID: 42,
	}
	if _, err := syncer.Sync(t.Context(), p); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if len(auth.calls) != 1 || auth.calls[0] != 42 {
		t.Errorf("resolver saw %v, want exactly one call for credential 42", auth.calls)
	}
}

// TestGitSyncer_APublicProjectAsksForNoCredential covers the ordinary case,
// where resolving one would be work and an audit record for nothing.
func TestGitSyncer_APublicProjectAsksForNoCredential(t *testing.T) {
	origin, _ := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})
	auth := &stubAuth{}
	syncer := project.NewGitSyncer(t.TempDir(), auth)

	p := project.Project{ID: 1, OrganizationID: 1, SCMType: project.SCMGit, SCMURL: origin}
	if _, err := syncer.Sync(t.Context(), p); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if len(auth.calls) != 0 {
		t.Errorf("resolver was asked %v for a project naming no credential", auth.calls)
	}
}

// TestGitSyncer_AnEncryptedKeyNeedsItsPassphrase is the case the user has
// to get right and the one a bad error message makes unfixable.
//
// An encrypted key with no passphrase must fail as a key problem, promptly
// and legibly. The alternative in an unattended sync is worse than a
// failure: a transport that waits for a passphrase nobody is there to type.
func TestGitSyncer_AnEncryptedKeyNeedsItsPassphrase(t *testing.T) {
	const passphrase = "correct horse battery staple"
	origin, _ := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})
	key := newEncryptedKey(t, passphrase)

	for _, tc := range []struct {
		name    string
		auth    project.Auth
		wantErr bool
	}{
		{"no passphrase", project.Auth{PrivateKey: key}, true},
		{"wrong passphrase", project.Auth{PrivateKey: key, Passphrase: "not it"}, true},
		{"right passphrase", project.Auth{PrivateKey: key, Passphrase: passphrase}, false},
		{"an unencrypted key needs none", project.Auth{PrivateKey: newPlainKey(t)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			syncer := project.NewGitSyncer(t.TempDir(), &stubAuth{auth: tc.auth})
			p := project.Project{
				ID: 1, OrganizationID: 1, SCMType: project.SCMGit,
				SCMURL: origin, CredentialID: 1,
			}
			got, err := syncer.Sync(t.Context(), p)
			if err != nil {
				t.Fatalf("Sync() = %v, want the outcome in the Result", err)
			}

			if tc.wantErr {
				if got.Status != project.SyncFailed {
					t.Fatalf("Sync() status = %q, want failed for a key that cannot be read", got.Status)
				}
				if !strings.Contains(got.Err, "SSH key could not be read") {
					t.Errorf("Sync() error = %q, want it to name the key as the problem", got.Err)
				}
				// The passphrase must never reach a stored, rendered
				// failure, whether it was right or wrong.
				if strings.Contains(got.Err, passphrase) || strings.Contains(got.Err, "not it") {
					t.Errorf("the recorded failure carries the passphrase:\n%s", got.Err)
				}
				return
			}

			// A readable key over a file:// origin does not authenticate
			// anything, so this only has to get PAST the key parse. A
			// failure here naming the key would mean a good key was
			// rejected.
			if strings.Contains(got.Err, "SSH key could not be read") {
				t.Errorf("a readable key was rejected: %s", got.Err)
			}
		})
	}
}

// TestGitSyncer_AResolverFailureIsRecordedWithoutDetail covers a credential
// that cannot be read at all.
//
// It is a recorded sync failure rather than a returned error, because the
// request to sync succeeded and the credential did not: that is the same
// distinction an unreachable repository already draws.
func TestGitSyncer_AResolverFailureIsRecordedWithoutDetail(t *testing.T) {
	origin, _ := newRepo(t, map[string]string{"site.yml": "- hosts: all\n"})
	auth := &stubAuth{err: errFakeResolver}
	syncer := project.NewGitSyncer(t.TempDir(), auth)

	p := project.Project{
		ID: 1, OrganizationID: 1, SCMType: project.SCMGit,
		SCMURL: origin, CredentialID: 7,
	}
	got, err := syncer.Sync(t.Context(), p)
	if err != nil {
		t.Fatalf("Sync() = %v, want the failure in the Result", err)
	}
	if got.Status != project.SyncFailed {
		t.Errorf("Sync() status = %q, want failed when the credential cannot be resolved", got.Status)
	}
	if got.Err == "" {
		t.Error("nothing was recorded, so an operator is told only that it did not work")
	}
}

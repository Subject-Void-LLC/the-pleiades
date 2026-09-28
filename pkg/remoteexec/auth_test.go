package remoteexec

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestAuthFrom is a table-driven test of the one place this platform
// decides which authentication method a set of stored material produces:
// password first, private key second, refusal when there is neither.
func TestAuthFrom(t *testing.T) {
	plainKeyPEM := marshalTestPrivateKey(t, "")
	encryptedKeyPEM := marshalTestPrivateKey(t, "correct-horse")

	tests := []struct {
		name       string
		user       string
		password   string
		keyPEM     []byte
		passphrase string
		wantErr    bool
	}{
		{
			name:     "password",
			user:     "u",
			password: "p",
		},
		{
			name:   "unencrypted private key",
			user:   "u",
			keyPEM: plainKeyPEM,
		},
		{
			name:       "passphrase-encrypted private key with correct passphrase",
			user:       "u",
			keyPEM:     encryptedKeyPEM,
			passphrase: "correct-horse",
		},
		{
			// Both are offered, the key first; TestAuthFrom_KeyAndPassword
			// proves the order against a real server.
			name:     "a private key and a password",
			user:     "u",
			password: "p",
			keyPEM:   plainKeyPEM,
		},
		{
			// A broken key beside a password is refused, not skipped.
			name:     "an unparsable key beside a password",
			user:     "u",
			password: "p",
			keyPEM:   []byte("garbage"),
			wantErr:  true,
		},
		{
			name:       "passphrase-encrypted private key with wrong passphrase",
			user:       "u",
			keyPEM:     encryptedKeyPEM,
			passphrase: "wrong",
			wantErr:    true,
		},
		{
			name:    "unparsable private key bytes",
			user:    "u",
			keyPEM:  []byte("garbage"),
			wantErr: true,
		},
		{
			name:    "neither password nor key set",
			user:    "u",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := AuthFrom(tc.user, tc.password, tc.keyPEM, tc.passphrase)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if auth.usable() {
					t.Error("expected an unusable Auth alongside a non-nil error: a usable one would let an unauthenticated dial proceed")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !auth.usable() {
				t.Fatal("expected a usable Auth")
			}
			if auth.user != tc.user {
				t.Errorf("Auth.user = %q, want %q", auth.user, tc.user)
			}
		})
	}
}

// TestAuthFromSecrets proves the flattened secret map a Collection
// method receives reaches AuthFrom through the shared wire.Secret* keys,
// and that a map with no usable material is refused rather than turned
// into an unauthenticated attempt.
func TestAuthFromSecrets(t *testing.T) {
	keyPEM := marshalTestPrivateKey(t, "")

	tests := []struct {
		name     string
		secrets  map[string]string
		wantErr  bool
		wantUser string
	}{
		{
			name:     "username and password",
			secrets:  map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "hunter2"},
			wantUser: "admin",
		},
		{
			name:     "username and private key",
			secrets:  map[string]string{wire.SecretUsername: "admin", wire.SecretPrivateKeyPEM: string(keyPEM)},
			wantUser: "admin",
		},
		{
			name:    "username only",
			secrets: map[string]string{wire.SecretUsername: "admin"},
			wantErr: true,
		},
		{
			// Flatten omits an empty field rather than writing "", so an
			// entirely absent map is the real shape of "this device has no
			// stored credential."
			name:    "no secrets at all",
			secrets: nil,
			wantErr: true,
		},
		{
			name:    "unparsable private key",
			secrets: map[string]string{wire.SecretUsername: "admin", wire.SecretPrivateKeyPEM: "not a real key"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := AuthFromSecrets(tc.secrets)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !auth.usable() {
				t.Fatal("expected a usable Auth")
			}
			if auth.user != tc.wantUser {
				t.Errorf("Auth.user = %q, want %q", auth.user, tc.wantUser)
			}
		})
	}
}

// TestAuthFromSecrets_ReadsAnEncryptedKeyWithItsPassphrase covers the
// one branch the table above cannot reach with a single map value: a key
// and its passphrase arriving together.
func TestAuthFromSecrets_ReadsAnEncryptedKeyWithItsPassphrase(t *testing.T) {
	encrypted := marshalTestPrivateKey(t, "correct-horse")

	auth, err := AuthFromSecrets(map[string]string{
		wire.SecretUsername:      "admin",
		wire.SecretPrivateKeyPEM: string(encrypted),
		wire.SecretPassphrase:    "correct-horse",
	})
	if err != nil {
		t.Fatalf("AuthFromSecrets with an encrypted key and its passphrase: %v", err)
	}
	if !auth.usable() {
		t.Fatal("expected a usable Auth")
	}

	if _, err := AuthFromSecrets(map[string]string{
		wire.SecretUsername:      "admin",
		wire.SecretPrivateKeyPEM: string(encrypted),
		wire.SecretPassphrase:    "wrong",
	}); err == nil {
		t.Fatal("expected a wrong passphrase to fail rather than silently produce an unusable signer")
	}
}

// TestAuth_KeepsTheSecretUnreadable is the reason Auth exists as a type
// rather than as a bare ssh.AuthMethod plus a username.
//
// A Collection method holds this value while it builds an error message,
// and a struct with an exported Password field is one %v away from
// putting a device credential in a job log. Formatting an Auth must not
// reveal the password it was built from.
func TestAuth_KeepsTheSecretUnreadable(t *testing.T) {
	const password = "s3cr3t-do-not-print"

	auth := PasswordAuth("admin", password)
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		rendered := fmt.Sprintf(format, auth)
		if strings.Contains(rendered, password) {
			t.Errorf("fmt %s of an Auth rendered the password: %q", format, rendered)
		}
	}
}

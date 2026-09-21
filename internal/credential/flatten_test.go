package credential

import (
	"context"
	"reflect"
	"testing"
)

func TestUnflatten(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want Credential
	}{
		{
			name: "username and password",
			in:   map[string]string{SecretUsername: "admin", SecretPassword: "hunter2"},
			want: Credential{Username: "admin", Password: "hunter2"},
		},
		{
			name: "key auth with passphrase",
			in: map[string]string{
				SecretUsername:      "admin",
				SecretPrivateKeyPEM: "-----BEGIN OPENSSH PRIVATE KEY-----",
				SecretPassphrase:    "keypass",
			},
			want: Credential{
				Username:      "admin",
				PrivateKeyPEM: []byte("-----BEGIN OPENSSH PRIVATE KEY-----"),
				Passphrase:    "keypass",
			},
		},
		{
			name: "a client certificate and its key",
			in: map[string]string{
				SecretCertificatePEM: "-----BEGIN CERTIFICATE-----",
				SecretPrivateKeyPEM:  "-----BEGIN PRIVATE KEY-----",
			},
			want: Credential{
				CertificatePEM: []byte("-----BEGIN CERTIFICATE-----"),
				PrivateKeyPEM:  []byte("-----BEGIN PRIVATE KEY-----"),
			},
		},
		{
			name: "a sealed bundle and its passphrase",
			in: map[string]string{
				SecretPFXBase64:  "MIIKzQIBAzCCCoc=",
				SecretPassphrase: "a-real-passphrase",
			},
			want: Credential{PFXBase64: "MIIKzQIBAzCCCoc=", Passphrase: "a-real-passphrase"},
		},
		{
			name: "empty map",
			in:   map[string]string{},
			want: Credential{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Unflatten(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Unflatten(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// TestFlattenUnflattenRoundTrip proves Flatten and Unflatten are genuine
// inverses for every field a Credential can carry, since NewStaticStore's
// whole correctness depends on that round trip being lossless.
func TestFlattenUnflattenRoundTrip(t *testing.T) {
	original := Credential{
		Username:       "admin",
		Password:       "hunter2",
		PrivateKeyPEM:  []byte("-----BEGIN OPENSSH PRIVATE KEY-----"),
		Passphrase:     "keypass",
		CertificatePEM: []byte("-----BEGIN CERTIFICATE-----"),
		PFXBase64:      "MIIKzQIBAzCCCoc=",
	}

	got := Unflatten(Flatten(original))
	if !reflect.DeepEqual(got, original) {
		t.Errorf("Unflatten(Flatten(%+v)) = %+v, want the original value back", original, got)
	}
}

func TestFlatten(t *testing.T) {
	tests := []struct {
		name string
		in   Credential
		want map[string]string
	}{
		{
			name: "username and password",
			in:   Credential{Username: "admin", Password: "hunter2"},
			want: map[string]string{SecretUsername: "admin", SecretPassword: "hunter2"},
		},
		{
			name: "key auth with passphrase",
			in: Credential{
				Username:      "admin",
				PrivateKeyPEM: []byte("-----BEGIN OPENSSH PRIVATE KEY-----"),
				Passphrase:    "keypass",
			},
			want: map[string]string{
				SecretUsername:      "admin",
				SecretPrivateKeyPEM: "-----BEGIN OPENSSH PRIVATE KEY-----",
				SecretPassphrase:    "keypass",
			},
		},
		{
			name: "a client certificate and its key",
			in: Credential{
				CertificatePEM: []byte("-----BEGIN CERTIFICATE-----"),
				PrivateKeyPEM:  []byte("-----BEGIN PRIVATE KEY-----"),
			},
			want: map[string]string{
				SecretCertificatePEM: "-----BEGIN CERTIFICATE-----",
				SecretPrivateKeyPEM:  "-----BEGIN PRIVATE KEY-----",
			},
		},
		{
			// No username, which is the shape a certificate credential
			// really has: the target maps the certificate to an account.
			name: "a sealed bundle and its passphrase",
			in: Credential{
				PFXBase64:  "MIIKzQIBAzCCCoc=",
				Passphrase: "a-real-passphrase",
			},
			want: map[string]string{
				SecretPFXBase64:  "MIIKzQIBAzCCCoc=",
				SecretPassphrase: "a-real-passphrase",
			},
		},
		{
			name: "zero value produces no keys, not empty-string values",
			in:   Credential{},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Flatten(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Flatten(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// TestEveryCredentialFieldSurvivesTheFileStore is the regression test for
// FAILURE_PATTERNS.md #274.
//
// Flatten and Unflatten agreeing is not enough, and that gap is exactly how
// the defect shipped: Phase 78d added CertificatePEM and PFXBase64 to
// Credential and taught Flatten about them, but left the file store's own
// entry struct at four fields. `add-credential --certificate` then printed
// "stored credential" and wrote nothing, and the failure surfaced later as
// an authentication error naming the one thing the operator had supplied.
//
// So this asserts the whole round trip through the REAL on-disk store
// rather than through the map alone. A field added to Credential without a
// column to hold it fails here.
func TestEveryCredentialFieldSurvivesTheFileStore(t *testing.T) {
	dir := t.TempDir()

	key, err := ResolveMasterKey(dir)
	if err != nil {
		t.Fatalf("ResolveMasterKey() error = %v", err)
	}

	want := Credential{
		Username:       "operator",
		Password:       "a-real-password",
		PrivateKeyPEM:  []byte("-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n"),
		Passphrase:     "a-real-passphrase",
		CertificatePEM: []byte("-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----\n"),
		PFXBase64:      "MIIKzQIBAzCCCoc=",
	}
	if err := SaveFileStore(dir, key, "win01", want); err != nil {
		t.Fatalf("SaveFileStore() error = %v", err)
	}

	store, err := NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	got, err := store.Lookup(context.Background(), "win01")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the credential did not survive the store:\n got %+v\nwant %+v", got, want)
	}

	// Named per field, because a DeepEqual failure on a redacted type prints
	// two identical-looking values and says nothing about which field went
	// missing. That is precisely how the original defect read.
	for _, f := range []struct {
		name      string
		got, want string
	}{
		{"username", got.Username, want.Username},
		{"password", got.Password, want.Password},
		{"private key", string(got.PrivateKeyPEM), string(want.PrivateKeyPEM)},
		{"passphrase", got.Passphrase, want.Passphrase},
		{"certificate", string(got.CertificatePEM), string(want.CertificatePEM)},
		{"bundle", got.PFXBase64, want.PFXBase64},
	} {
		if f.got != f.want {
			t.Errorf("the %s did not survive the store: got %d bytes, want %d", f.name, len(f.got), len(f.want))
		}
	}
}

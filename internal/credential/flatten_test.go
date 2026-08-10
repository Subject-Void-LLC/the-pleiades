package credential

import (
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
			want: Credential{Username: "admin", Password: "hunter2", PrivateKeyPEM: []byte{}},
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
			name: "empty map",
			in:   map[string]string{},
			want: Credential{PrivateKeyPEM: []byte{}},
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
		Username:      "admin",
		Password:      "hunter2",
		PrivateKeyPEM: []byte("-----BEGIN OPENSSH PRIVATE KEY-----"),
		Passphrase:    "keypass",
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

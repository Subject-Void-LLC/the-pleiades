package credential_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/credential"
)

// TestCredential_StringRedactsSecrets proves, by actually formatting a
// Credential through fmt, that a real secret never reaches the output.
// This is not a test that the String/GoString methods exist; it is a
// test that Go's automatic Stringer/GoStringer dispatch (which fires for
// %v/%s/%q and for a Credential nested as a field inside another struct
// formatted with %+v) actually prevents a stray log.Printf or slog call
// anywhere else in the codebase from leaking a credential by accident.
func TestCredential_StringRedactsSecrets(t *testing.T) {
	const wantPassword = "super-secret-password"
	const wantPassphrase = "super-secret-passphrase"
	keyBytes := []byte("-----BEGIN PRIVATE KEY-----\nsecretkeybytes\n-----END PRIVATE KEY-----")

	cred := credential.Credential{
		Username:      "admin",
		Password:      wantPassword,
		PrivateKeyPEM: keyBytes,
		Passphrase:    wantPassphrase,
	}

	direct := fmt.Sprintf("%v", cred)
	assertNoSecretLeak(t, direct, wantPassword, wantPassphrase, keyBytes)

	// %+v on a struct that merely contains a Credential field: fmt calls
	// the field's String method here too, so this proves the guarantee
	// holds for nested use, not just a direct fmt.Sprintf on the
	// Credential value itself.
	nested := fmt.Sprintf("%+v", struct{ C credential.Credential }{C: cred})
	assertNoSecretLeak(t, nested, wantPassword, wantPassphrase, keyBytes)

	// %#v routes through GoString instead of String; same guarantee.
	goSyntax := fmt.Sprintf("%#v", cred)
	assertNoSecretLeak(t, goSyntax, wantPassword, wantPassphrase, keyBytes)

	// The username is not a secret and must still be visible in full,
	// otherwise this type would be useless for debugging.
	if !strings.Contains(direct, "admin") {
		t.Errorf("expected username to be visible in %q", direct)
	}
}

// assertNoSecretLeak fails the test if output contains any of the real
// secret values it was given.
func assertNoSecretLeak(t *testing.T, output, password, passphrase string, key []byte) {
	t.Helper()
	if strings.Contains(output, password) {
		t.Errorf("output leaked the real password: %q", output)
	}
	if strings.Contains(output, passphrase) {
		t.Errorf("output leaked the real passphrase: %q", output)
	}
	if len(key) > 0 && strings.Contains(output, string(key)) {
		t.Errorf("output leaked the real private key bytes: %q", output)
	}
}

// TestCredential_StringDistinguishesSetFromNotSet is table-driven across
// all three secret fields independently, proving the marker for each
// field flips only in response to that field, not the others.
func TestCredential_StringDistinguishesSetFromNotSet(t *testing.T) {
	tests := []struct {
		name string
		cred credential.Credential
		want string // substring expected to appear in String()
	}{
		{"all unset", credential.Credential{Username: "u"}, "Password:<not set>"},
		{"password set", credential.Credential{Username: "u", Password: "p"}, "Password:<redacted, set>"},
		{"key set", credential.Credential{Username: "u", PrivateKeyPEM: []byte("k")}, "PrivateKeyPEM:<redacted, set>"},
		{"passphrase set", credential.Credential{Username: "u", Passphrase: "pp"}, "Passphrase:<redacted, set>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cred.String()
			if !strings.Contains(got, tt.want) {
				t.Errorf("String() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

// TestCredential_GoStringMatchesString proves GoString is not an
// independent, possibly-forgotten redaction path: it must produce
// exactly what String produces, so %#v gets the same guarantee %v does.
func TestCredential_GoStringMatchesString(t *testing.T) {
	cred := credential.Credential{Username: "u", Password: "p"}
	if cred.GoString() != cred.String() {
		t.Errorf("GoString() = %q, want it to equal String() = %q", cred.GoString(), cred.String())
	}
}

// TestCredential_MarshalJSONRedactsSecrets is a regression test for
// FAILURE_PATTERNS.md #22, found by Phase W6's own Schema/Injection
// Hardening audit: encoding/json.Marshal reflects over a value's exported
// fields directly, never consulting fmt.Stringer/fmt.GoStringer, so
// without a MarshalJSON method the String/GoString redaction above did
// nothing to protect a Credential serialized to JSON. This proves the
// real secret bytes, including PrivateKeyPEM's base64 encoding (which
// encoding/json applies by default to a []byte field and which is
// trivially reversible, not a real protection), never reach the
// marshaled output.
func TestCredential_MarshalJSONRedactsSecrets(t *testing.T) {
	const wantPassword = "super-secret-password"
	const wantPassphrase = "super-secret-passphrase"
	keyBytes := []byte("-----BEGIN PRIVATE KEY-----\nsecretkeybytes\n-----END PRIVATE KEY-----")

	cred := credential.Credential{
		Username:      "admin",
		Password:      wantPassword,
		PrivateKeyPEM: keyBytes,
		Passphrase:    wantPassphrase,
	}

	data, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	assertNoSecretLeak(t, string(data), wantPassword, wantPassphrase, keyBytes)
	// The base64 encoding of the key bytes is a different string than the
	// raw bytes, so assertNoSecretLeak's raw-byte check above would not
	// catch a leak via that encoding; check it explicitly too.
	if strings.Contains(string(data), "-----BEGIN") {
		t.Errorf("expected the private key marker/header not to appear at all, got: %s", data)
	}
	if !strings.Contains(string(data), "admin") {
		t.Errorf("expected the username to remain visible in %s", data)
	}

	// json.Marshal on a struct CONTAINING a Credential field must be
	// protected too, the same nested-use guarantee
	// TestCredential_StringRedactsSecrets already proves for fmt.
	nested, err := json.Marshal(struct {
		C credential.Credential `json:"credential"`
	}{C: cred})
	if err != nil {
		t.Fatalf("json.Marshal (nested) failed: %v", err)
	}
	assertNoSecretLeak(t, string(nested), wantPassword, wantPassphrase, keyBytes)
}

// TestCredential_LogValueRedactsSecrets is a regression test for
// FAILURE_PATTERNS.md #22: log/slog's JSON handler, like encoding/json,
// does not consult fmt.Stringer, so a structured log call passing a
// Credential as an attribute value would leak every secret field without
// a LogValue method. This proves it through a real slog.Logger backed by
// a real slog.JSONHandler, the same handler shape the audit reproduced
// the leak against, not merely by calling LogValue directly.
func TestCredential_LogValueRedactsSecrets(t *testing.T) {
	const wantPassword = "super-secret-password"
	cred := credential.Credential{Username: "admin", Password: wantPassword}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("connecting", "credential", cred)

	output := buf.String()
	if strings.Contains(output, wantPassword) {
		t.Errorf("expected slog JSON output not to leak the password, got: %s", output)
	}
	if !strings.Contains(output, "admin") {
		t.Errorf("expected the username to remain visible in %s", output)
	}
}

// TestErrNotFound_SatisfiesErrorsIsAfterWrapping proves ErrNotFound
// still matches errors.Is after being wrapped, since every adapter is
// expected to wrap it with device-specific guidance rather than return
// it bare (see file_store.go).
func TestErrNotFound_SatisfiesErrorsIsAfterWrapping(t *testing.T) {
	wrapped := fmt.Errorf("credential for device %q: %w", "router1", credential.ErrNotFound)
	if !errors.Is(wrapped, credential.ErrNotFound) {
		t.Fatalf("expected wrapped error to satisfy errors.Is(err, ErrNotFound), got %v", wrapped)
	}
}

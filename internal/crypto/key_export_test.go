// Tests for the key generator, decoder, encoder and fingerprint the resolver
// exports.
package crypto_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// TestGenerateKey_ReturnsDistinctKeysOfTheRequiredSize proves the one
// generator produces what the controller's own loader accepts: a key that
// round-trips through EncodeKey and DecodeKey at exactly 32 bytes.
func TestGenerateKey_ReturnsDistinctKeysOfTheRequiredSize(t *testing.T) {
	a, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	b, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("GenerateKey() returned %d and %d bytes, want 32", len(a), len(b))
	}
	if string(a) == string(b) {
		t.Fatal("GenerateKey() returned the same key twice")
	}

	back, err := crypto.DecodeKey(crypto.EncodeKey(a), "test")
	if err != nil {
		t.Fatalf("DecodeKey(EncodeKey(key)) error = %v", err)
	}
	if string(back) != string(a) {
		t.Fatal("DecodeKey(EncodeKey(key)) did not return the key")
	}
}

// TestDecodeKey_NeverEchoesTheValue proves the refusal names where the value
// came from and never the value itself, since a rejected value is often a
// real key one character off.
func TestDecodeKey_NeverEchoesTheValue(t *testing.T) {
	cases := map[string]string{
		"not base64":   "this-is-a-long-secret-looking-value!!",
		"wrong size":   crypto.EncodeKey([]byte(strings.Repeat("x", 16))),
		"empty":        "",
		"almost a key": crypto.EncodeKey([]byte(strings.Repeat("y", 32)))[:40],
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := crypto.DecodeKey(raw, "the test source")
			if err == nil {
				t.Fatalf("DecodeKey(%s) accepted it", name)
			}
			if raw != "" && strings.Contains(err.Error(), raw) {
				t.Fatalf("DecodeKey error %q contains the value it refused", err)
			}
			if !strings.Contains(err.Error(), "the test source") {
				t.Fatalf("DecodeKey error %q does not name its source", err)
			}
		})
	}
}

// TestFingerprint_IsStableDistinctAndDomainSeparated pins the three
// properties the setup command and the encryption key registry rely on.
func TestFingerprint_IsStableDistinctAndDomainSeparated(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	other := []byte(strings.Repeat("j", 32))

	if crypto.Fingerprint(key) != crypto.Fingerprint(key) {
		t.Fatal("Fingerprint is not stable for the same key")
	}
	if crypto.Fingerprint(key) == crypto.Fingerprint(other) {
		t.Fatal("two different keys share a fingerprint")
	}
	if got := len(crypto.Fingerprint(key)); got != 64 {
		t.Fatalf("Fingerprint is %d characters, want 64 hex characters", got)
	}

	// A plain SHA-256 of the key is a value some other code could compute
	// for some other purpose. The fingerprint must not equal it.
	plain := sha256.Sum256(key)
	if crypto.Fingerprint(key) == hex.EncodeToString(plain[:]) {
		t.Fatal("Fingerprint equals a plain SHA-256 of the key; the domain prefix is missing")
	}
	if strings.Contains(crypto.Fingerprint(key), crypto.EncodeKey(key)) {
		t.Fatal("Fingerprint contains the encoded key")
	}
}

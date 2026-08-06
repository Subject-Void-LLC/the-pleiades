package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"testing"
)

// White-box (package auth, not auth_test): parseJWK is unexported. These
// tests exercise it directly rather than through an export_test.go seam,
// since nothing outside this package needs to construct a raw jwk value.

func TestParseJWK_TableDriven(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}
	validN := base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes())
	validE := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}) // 65537
	validX := base64.RawURLEncoding.EncodeToString(ecKey.X.Bytes())
	validY := base64.RawURLEncoding.EncodeToString(ecKey.Y.Bytes())

	tests := []struct {
		name    string
		key     jwk
		wantErr bool
	}{
		{"valid RSA", jwk{Kty: "RSA", Kid: "k1", N: validN, E: validE}, false},
		{"valid EC P-256", jwk{Kty: "EC", Kid: "k1", Crv: "P-256", X: validX, Y: validY}, false},
		{"missing kid", jwk{Kty: "RSA", N: validN, E: validE}, true},
		{"unsupported kty", jwk{Kty: "oct", Kid: "k1"}, true},
		{"RSA missing n", jwk{Kty: "RSA", Kid: "k1", E: validE}, true},
		{"RSA missing e", jwk{Kty: "RSA", Kid: "k1", N: validN}, true},
		{"RSA malformed n base64", jwk{Kty: "RSA", Kid: "k1", N: "not-base64!!!", E: validE}, true},
		{"RSA implausible exponent length", jwk{Kty: "RSA", Kid: "k1", N: validN, E: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}, true},
		{"EC missing crv", jwk{Kty: "EC", Kid: "k1", X: validX, Y: validY}, true},
		{"EC unsupported crv", jwk{Kty: "EC", Kid: "k1", Crv: "P-999", X: validX, Y: validY}, true},
		{"EC missing x", jwk{Kty: "EC", Kid: "k1", Crv: "P-256", Y: validY}, true},
		{"EC missing y", jwk{Kty: "EC", Kid: "k1", Crv: "P-256", X: validX}, true},
		{"EC point not on curve", jwk{Kty: "EC", Kid: "k1", Crv: "P-256", X: validX, Y: validX}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseJWK(tc.key)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseJWK(%+v) error = %v, wantErr %v", tc.key, err, tc.wantErr)
			}
		})
	}
}

// FuzzParseJWK proves parseJWK never panics on adversarial field values
// (malformed base64, wrong kty, an implausibly long exponent, a garbage
// curve name), across every field a JWKS response controls.
func FuzzParseJWK(f *testing.F) {
	f.Add("RSA", "k1", "AQAB", "AQAB", "", "", "")
	f.Add("EC", "k1", "", "", "P-256", "AQAB", "AQAB")
	f.Add("oct", "k1", "", "", "", "", "")
	f.Add("", "", "", "", "", "", "")
	f.Add("RSA", "k1", "not-base64!!!", "AQAB", "", "", "")

	f.Fuzz(func(t *testing.T, kty, kid, n, e, crv, x, y string) {
		// Only the absence of a panic is asserted: every combination here
		// is either a real key (which must parse) or malformed input
		// (which must return an error, never panic).
		_, _ = parseJWK(jwk{Kty: kty, Kid: kid, N: n, E: e, Crv: crv, X: x, Y: y})
	})
}

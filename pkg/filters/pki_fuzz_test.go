package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// This file fuzzes every parser in pki.go against malformed and
// truncated input, per this phase's own Fuzz/Stress Test checklist item
// naming the JWT, X.509, PEM/DER and SSH-key parsing functions
// explicitly. ParseDistinguishedName is included too even though the
// checklist does not name it by category: it parses untrusted,
// structurally rich text the same way the four named functions do, and
// every non-trivial parser in this package gets a fuzz target.

func FuzzParseJWTPayloadUnverified(f *testing.F) {
	seeds := []string{
		"", "not-a-jwt", "a.b.c", "a.b",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEyMyJ9.c2lnbmF0dXJl",
		"..", "a.b.c.d",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, token string) {
		filters.ParseJWTPayloadUnverified(token)
	})
}

func FuzzParseX509Certificate(f *testing.F) {
	seeds := []string{
		"", "not a pem block",
		"-----BEGIN CERTIFICATE-----\nbm90IHJlYWwgZGVy\n-----END CERTIFICATE-----\n",
		"-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, pemCert string) {
		filters.ParseX509Certificate(pemCert)
	})
}

func FuzzPEMToDER(f *testing.F) {
	seeds := []string{
		"", "not a pem block",
		"-----BEGIN CERTIFICATE-----\nbm90IHJlYWwgZGVy\n-----END CERTIFICATE-----\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, pemStr string) {
		filters.PEMToDER(pemStr)
	})
}

func FuzzDERToPEM(f *testing.F) {
	seeds := []struct{ der, blockType string }{
		{"", "CERTIFICATE"},
		{"bm90IHJlYWwgZGVy", "CERTIFICATE"},
		{"not base64!!", "PUBLIC KEY"},
		{"bm90IHJlYWwgZGVy", "CERT\nIFICATE"},
		{"bm90IHJlYWwgZGVy", ""},
	}
	for _, s := range seeds {
		f.Add(s.der, s.blockType)
	}
	f.Fuzz(func(t *testing.T, der, blockType string) {
		filters.DERToPEM(der, blockType)
	})
}

func FuzzSSHPublicKeyToPEM(f *testing.F) {
	seeds := []string{
		"", "not an ssh key",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINp0",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, authorizedKey string) {
		filters.SSHPublicKeyToPEM(authorizedKey)
	})
}

func FuzzPEMToSSHPublicKey(f *testing.F) {
	seeds := []string{
		"", "not a pem block",
		"-----BEGIN PUBLIC KEY-----\nbm90IHJlYWwgZGVy\n-----END PUBLIC KEY-----\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, pemStr string) {
		filters.PEMToSSHPublicKey(pemStr)
	})
}

func FuzzParseDistinguishedName(f *testing.F) {
	seeds := []string{
		"", "CN=John Doe,OU=Sales,DC=example,DC=com",
		`CN=Doe\, John,OU=Sales`, "OU=Sales+L=NYC", "CN=#04024869",
		"NotAKeyValuePair", `CN=John\`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, dn string) {
		filters.ParseDistinguishedName(dn)
	})
}

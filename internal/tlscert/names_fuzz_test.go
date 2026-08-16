// The fuzz target this package owed, aimed at the one surface where a bad
// input used to travel a long way before failing.
//
// namesFrom takes a comma-split environment variable and turns it into the
// two subject alternative name fields. Everything it produces goes straight
// into x509.CreateCertificate, which encodes DNS names as IA5Strings and
// rejects anything outside ASCII with a message that names neither the value
// nor the setting. So the property worth fuzzing is not "namesFrom does not
// panic", which is nearly free. It is the contract between the two functions:
//
//	if ValidateNames accepts a list, signing a certificate with the names
//	namesFrom derives from it must succeed.
//
// A checker that is too strict fails nothing and quietly refuses names that
// work; TestValidateNamesAcceptsWhatDeploymentsReallyUse is the control for
// that direction. A checker that is too loose lets a value through to the
// deep failure, which is what this catches.
//
// The test lives in package tlscert rather than tlscert_test because
// namesFrom is unexported, and it is the function whose output reaches x509;
// fuzzing only the exported wrapper would leave the derivation itself
// uncovered.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"
)

// FuzzNamesFrom drives the split, the check and the signing together.
func FuzzNamesFrom(f *testing.F) {
	// Seeds are the shapes a real PLEIADES_TLS_AUTOCERT_HOSTS takes, plus
	// the ones that broke: an international domain in display form, a
	// non-breaking space, an over-long label, an empty label.
	for _, seed := range []string{
		"",
		"controller",
		"controller, pleiades.example.test ,10.9.8.7",
		"localhost,127.0.0.1,::1",
		"*.example.test",
		"contrôleur.example.test",
		"controller .example.test",
		strings.Repeat("a", 300),
		"a..b",
		"-leading.example.test",
		"trailing-.example.test",
		"fd00::1,fd00::1,FD00::1",
		",,,",
		"\x00",
		"host_1.internal,HOST_1.INTERNAL",
	} {
		f.Add(seed)
	}

	// One key for the whole run: generating a P-256 key per iteration would
	// make this a benchmark of ecdsa.GenerateKey rather than a fuzz of the
	// names.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatalf("generating the signing key: %v", err)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		// The exact treatment cmd/controller gives the environment variable:
		// split on commas and hand the pieces over without pre-cleaning.
		entries := strings.Split(raw, ",")

		if err := ValidateNames(entries); err != nil {
			// Rejected at the edge, which is the whole point. There is
			// nothing left to check, and the error is required to quote what
			// was wrong so an operator can find it.
			if !strings.Contains(err.Error(), "not a name a certificate can carry") {
				t.Errorf("ValidateNames(%q) rejected without saying what it rejects: %v", raw, err)
			}
			return
		}

		dnsNames, ipAddresses := namesFrom(entries)

		// The loopback set is unconditional, so an input that removed it
		// would produce a certificate no local client could verify.
		if len(dnsNames) == 0 || dnsNames[0] != "localhost" {
			t.Fatalf("namesFrom(%q) = %v, which does not start with localhost", raw, dnsNames)
		}
		if len(ipAddresses) < 2 {
			t.Fatalf("namesFrom(%q) returned %v, which is missing a loopback address", raw, ipAddresses)
		}
		assertNoDuplicates(t, raw, dnsNames)

		// The contract this exists to hold: everything ValidateNames let
		// through has to survive signing.
		template := x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "pleiades-local"},
			NotBefore:             time.Now().Add(-time.Minute),
			NotAfter:              time.Now().Add(time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
			IsCA:                  true,
			DNSNames:              dnsNames,
			IPAddresses:           ipAddresses,
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			t.Fatalf("ValidateNames accepted %q but x509 refused to sign the names it produced (%v, %v): %v",
				raw, dnsNames, ipAddresses, err)
		}
		// Parsed back, because a name that encodes and does not decode is
		// the same outage as one that never encoded.
		if _, err := x509.ParseCertificate(der); err != nil {
			t.Fatalf("the certificate signed for %q does not parse back: %v", raw, err)
		}
	})
}

// assertNoDuplicates fails when the same DNS name appears twice.
//
// De-duplication is not cosmetic: namesFrom exists partly so that a value
// written "controller,Controller" produces one SAN rather than two that no
// resolver can tell apart, and a duplicate would also make the reuse check
// compare against a list longer than the certificate's.
func assertNoDuplicates(t *testing.T, raw string, names []string) {
	t.Helper()
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			t.Fatalf("namesFrom(%q) returned %q twice", raw, name)
		}
		seen[name] = true
	}
}

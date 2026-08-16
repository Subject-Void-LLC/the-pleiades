// The proof that this package's wrapper really is the shared generator
// with a shorter lifetime, and not a second implementation.
//
// The deep proofs (a real handshake by both loopback names, a control that
// trusts nothing, the file modes, the SAN sorting) live beside the code
// they cover, in internal/tlscert. Repeating them here would test the same
// function twice and would go stale in the copy nobody is looking at. What
// is left is the only behavior this file adds: the paths are real, and the
// lifetime is the throwaway one.
package testsupport_test

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// TestNewServingCertWritesALoadablePair proves the two paths handed back
// really name a certificate and the key that goes with it, which is the
// whole contract every caller of this helper relies on.
func TestNewServingCertWritesALoadablePair(t *testing.T) {
	cert := testsupport.NewServingCertFor(t, t.TempDir())

	if _, err := tls.LoadX509KeyPair(cert.CertFile, cert.KeyFile); err != nil {
		t.Fatalf("the generated pair does not load: %v", err)
	}
	if cert.Roots == nil {
		t.Error("no trust pool was returned, so a client has nothing to verify this certificate against")
	}
}

// TestNewServingCertIsShortLived is the one thing this package decides for
// itself. Everything it generates is thrown away at the end of a test run
// or a development session, so a long-lived key left in a scratch
// directory is a liability with no upside.
func TestNewServingCertIsShortLived(t *testing.T) {
	cert := testsupport.NewServingCertFor(t, t.TempDir())

	// Checked against the declared constant rather than a literal, so the
	// two cannot drift apart.
	lifetime := cert.Leaf.NotAfter.Sub(cert.Leaf.NotBefore)
	if lifetime > testsupport.ServingCertTTL+2*time.Minute {
		t.Errorf("the certificate is valid for %v, want about %v", lifetime, testsupport.ServingCertTTL)
	}

	// And the comparison that says why the constant exists at all: a
	// throwaway certificate must not inherit the lifetime the controller's
	// own persisted certificate is sized for.
	if testsupport.ServingCertTTL >= tlscert.DefaultTTL {
		t.Errorf("ServingCertTTL (%v) is not shorter than tlscert.DefaultTTL (%v), so this package is no longer generating a short-lived certificate",
			testsupport.ServingCertTTL, tlscert.DefaultTTL)
	}
}

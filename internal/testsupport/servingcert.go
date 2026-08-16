// The test-facing face of internal/tlscert: the same serving certificate
// generator every local HTTPS stack in this repository runs on, with the
// short lifetime and the testing.TB convenience a throwaway certificate
// wants.
//
// There is no generation logic here any more, and that is the point. It
// used to live in this package, which meant the only implementation of
// "generate a serving certificate" sat behind an import of testing. The
// controller then needed the same logic in order to provision a
// certificate for itself when an operator configured none, and importing
// this package from a shipped binary would have linked the testing package
// into it: test flags registered on the default flag set, and a larger
// image, for a function that is not test-specific at all. So the logic
// moved to internal/tlscert and this file calls it. Two implementations
// would have been the worse outcome of the two: a harness whose
// certificate was built differently from the one the controller serves
// proves TLS works for a certificate nobody runs.
package testsupport

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// ServingCertTTL bounds how long a certificate from this package stays
// valid.
//
// A week, and deliberately far shorter than tlscert.DefaultTTL, because
// these are not the same kind of certificate. The controller's own
// self-provisioned certificate is persisted and reused across restarts, so
// it is sized to outlive a normal upgrade cadence. Everything this package
// generates is thrown away at the end of a test run or a development
// session, so the only thing a long life buys is a stray copy of a private
// key that still works months later. A week is long enough that a
// development stack left up over a weekend still serves, which is the one
// real cost of going shorter: an expired certificate fails a handshake in
// a way that reads like a configuration mistake.
const ServingCertTTL = 7 * 24 * time.Hour

// ServingCert is tlscert.ServingCert under this package's older name.
//
// An alias rather than a wrapper struct, so a caller can keep reading
// CertFile, KeyFile and Roots and calling TLSClientConfig without this
// package restating any of them, and so a value produced here can be
// handed to anything that takes the tlscert type.
type ServingCert = tlscert.ServingCert

// NewServingCert generates a short-lived self-signed serving certificate
// into dir and returns the paths and the trust pool for it.
//
// Always a fresh certificate, never a reused one: tlscert.Ensure exists
// for the controller, which has to keep the same certificate across
// restarts, and every caller here wants a certificate that belongs to this
// run and disappears with it.
func NewServingCert(dir string) (ServingCert, error) {
	return tlscert.Generate(dir, tlscert.Options{TTL: ServingCertTTL})
}

// NewServingCertFor is NewServingCert for a test, failing the test rather
// than returning an error.
func NewServingCertFor(tb testing.TB, dir string) ServingCert {
	tb.Helper()
	cert, err := NewServingCert(dir)
	if err != nil {
		tb.Fatalf("generating a serving certificate in %s: %v", dir, err)
	}
	return cert
}

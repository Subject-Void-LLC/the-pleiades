// What the external tests reach inside the package: the key log's platform
// rule and the moment between a directory's check and the file's creation,
// and the bound on a diagnosis, so a test can prove a diagnosis ends at the
// caller's cancellation rather than at its own bound.
package devicetls

import (
	"crypto/x509"
	"testing"
	"time"
)

// Observe records cert on p as a verified handshake would, for a test of
// the record's bound without a server per certificate.
func Observe(p *Presented, cert *x509.Certificate) { p.observe(cert) }

// UnsupportedOn is unsupportedOn, for a test of the platform rule on any
// platform.
var UnsupportedOn = unsupportedOn

// SetAfterKeyLogCheck runs hook between a key log directory's check and the
// file's creation for the rest of t, so a test can change the path in
// between.
func SetAfterKeyLogCheck(t testing.TB, hook func()) {
	t.Helper()
	afterKeyLogCheck = hook
	t.Cleanup(func() { afterKeyLogCheck = nil })
}

// SetDiagnoseTimeout sets the bound on a diagnosis for t, restoring it when
// t ends.
func SetDiagnoseTimeout(t testing.TB, d time.Duration) {
	t.Helper()
	old := diagnoseTimeout
	diagnoseTimeout = d
	t.Cleanup(func() { diagnoseTimeout = old })
}

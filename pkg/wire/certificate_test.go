// Tests of the check a client certificate's PEM is held to before storage.
package wire_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// testCertificatePEM and testKeyPEM are a certificate block and a key block
// as an operator's bundle holds them; neither needs to parse for this rule.
const (
	testCertificatePEM = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ\n-----END CERTIFICATE-----\n"
	testKeyPEM         = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49\n-----END PRIVATE KEY-----\n"
)

// TestClientCertificateProblem_RefusesEveryBlockThatIsNotACertificate: a
// certificate, a chain, and text with no PEM at all are stored; a bundle
// holding a key beside the certificate is refused, in any order, and so is
// a key block too damaged or too indented for encoding/pem to decode.
func TestClientCertificateProblem_RefusesEveryBlockThatIsNotACertificate(t *testing.T) {
	for _, ok := range []string{testCertificatePEM, testCertificatePEM + testCertificatePEM, "not pem at all", ""} {
		if problem := wire.ClientCertificateProblem([]byte(ok)); problem != "" {
			t.Errorf("ClientCertificateProblem(%q) = %q, want it accepted", ok, problem)
		}
	}
	for _, bad := range []string{
		testCertificatePEM + testKeyPEM,
		testKeyPEM + testCertificatePEM,
		testKeyPEM,
		"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----\n",
		"-----BEGIN EC PRIVATE KEY-----\nMHcC\n-----END EC PRIVATE KEY-----\n",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl\n-----END OPENSSH PRIVATE KEY-----\n",
		"-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIF\n-----END ENCRYPTED PRIVATE KEY-----\n",
		testCertificatePEM + "   -----BEGIN PRIVATE KEY-----\n   MIGH\n   -----END PRIVATE KEY-----\n",
		testCertificatePEM + "-----BEGIN PRIVATE KEY-----\n%%% not base64 %%%\n",
		"-----BEGIN CERTIFICATE",
	} {
		if problem := wire.ClientCertificateProblem([]byte(bad)); problem == "" {
			t.Errorf("ClientCertificateProblem(%q) accepted a block that is not a certificate", bad)
		}
	}
}

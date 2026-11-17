// A test of both pin forms against hashes computed by a tool that is not
// this package: OpenSSL, over a certificate made for this test whose key was
// thrown away (testdata/spki-vector.pem).
package devicetls_test

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// TestPinForms_MatchOpenSSL: the key pin is the SHA-256 of the DER
// SubjectPublicKeyInfo and the certificate pin the SHA-256 of the DER
// certificate, as OpenSSL 3.0.2 computed them on 2026-11-16:
//
//	openssl x509 -in testdata/spki-vector.pem -pubkey -noout | openssl pkey -pubin -outform der | sha256sum
//	openssl x509 -in testdata/spki-vector.pem -outform der | sha256sum
//
// The certificate was made with
//
//	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout key.pem -subj /CN=spki-vector.pleiades.invalid -days 36500 -out spki-vector.pem
//
// and key.pem deleted at once.
func TestPinForms_MatchOpenSSL(t *testing.T) {
	data, err := os.ReadFile("testdata/spki-vector.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("testdata/spki-vector.pem holds no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := devicetls.KeyFingerprint(cert), "spki-sha256:7a7b045d2bb9bcf61e195dda0f0a77e6a40c9724c24012ddd2f3f71d669b74bf"; got != want {
		t.Errorf("KeyFingerprint = %s, OpenSSL computes %s", got, want)
	}
	if got, want := devicetls.Fingerprint(cert), "sha256:63d8d5cbd0d281837430fe3902ade1576d08121ab15564f24c954039f393288f"; got != want {
		t.Errorf("Fingerprint = %s, OpenSSL computes %s", got, want)
	}
}

// Tests for pinning one certificate against a real TLS server presenting
// an expired self-signed certificate, the shape of a lab controller's, and
// for naming the certificate a device presented when nothing trusts it.
package devicetls_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// expiredServer starts a TLS server presenting a self-signed certificate
// that expired a month ago, for a name the client never dials, and
// returns it with that certificate.
func expiredServer(t *testing.T) (*httptest.Server, *x509.Certificate) {
	t.Helper()
	return certServer(t, time.Now().Add(-30*24*time.Hour))
}

// certServer starts a TLS server presenting a self-signed certificate for
// controller.lab.invalid valid until notAfter, and returns it with that
// certificate.
func certServer(t *testing.T, notAfter time.Time) (*httptest.Server, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "controller.lab.invalid"},
		DNSNames:     []string{"controller.lab.invalid"},
		NotBefore:    time.Now().Add(-400 * 24 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, cert
}

// get fetches the server's root with settings' configuration, returning
// the request's error.
func get(t *testing.T, s devicetls.Settings, srv *httptest.Server) error {
	t.Helper()
	cfg, err := s.Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 30 * time.Second}
	resp, err := client.Get(srv.URL)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// TestPin_TrustsExactlyThePinnedCertificate: a pin of the server's own
// certificate reaches it, though the certificate expired, is self-signed
// and names another host; a pin of any other certificate is refused with
// the presented one's fingerprint; and with no pin at all Go's own checks
// refuse it, which Explain turns into the same description.
func TestPin_TrustsExactlyThePinnedCertificate(t *testing.T) {
	srv, cert := expiredServer(t)
	want := devicetls.Fingerprint(cert)

	pinned, err := parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: strings.ToUpper(want[len("sha256:"):])})
	if err == nil {
		t.Fatal("a fingerprint without its sha256: prefix was accepted")
	}
	if pinned, err = parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: "SHA256:" + strings.ToUpper(want[len("sha256:"):])}); err != nil {
		t.Fatal(err)
	}
	if pinned.PinnedFingerprint() != want {
		t.Errorf("the pin reads as %q, want %q", pinned.PinnedFingerprint(), want)
	}
	if err := get(t, pinned, srv); err != nil {
		t.Errorf("the pinned certificate was refused: %v", err)
	}

	other, err := parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: "sha256:" + strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	var untrusted *devicetls.UntrustedError
	if err := get(t, other, srv); !errors.As(err, &untrusted) || untrusted.Fingerprint != want || !strings.Contains(err.Error(), "not the pinned") {
		t.Errorf("another pin: %v, want an UntrustedError naming %s", err, want)
	}

	defaults, err := parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	err = get(t, defaults, srv)
	if err == nil {
		t.Fatal("an expired self-signed certificate was trusted with no pin")
	}
	explained := devicetls.Explain(err)
	if !errors.As(explained, &untrusted) || untrusted.Fingerprint != want || untrusted.NotAfter != cert.NotAfter.UTC().Format("2006-01-02") ||
		!strings.Contains(explained.Error(), "pin it as the device's tls_fingerprint property") {
		t.Errorf("Explain(%v) = %v, want an UntrustedError naming %s", err, explained, want)
	}
	if errors.Unwrap(explained) == nil {
		t.Error("the explanation lost its cause")
	}
	plain := errors.New("connection refused")
	if devicetls.Explain(plain) != plain {
		t.Error("Explain changed an error that is not about a certificate")
	}
}

// TestPin_Refusals: a pin that is not sha256 and 64 hex digits, or that is
// not text, is refused, and so is a pin beside a pinned authority, two
// answers to what the certificate is trusted by. A pin is warned about on
// every use, and is among the properties a device type dispatches.
func TestPin_Refusals(t *testing.T) {
	for name, v := range map[string]inventory.PropertyValue{
		"short":      "sha256:abcd",
		"not hex":    "sha256:" + strings.Repeat("zz", 32),
		"sha1":       "sha1:" + strings.Repeat("ab", 20),
		"not text":   true,
		"empty text": "",
	} {
		if _, err := parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: v}); err == nil || !strings.Contains(err.Error(), devicetls.FingerprintProperty) {
			t.Errorf("%s: %v, want a refusal naming the property", name, err)
		}
	}
	_, cert := expiredServer(t)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	if _, err := parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: devicetls.Fingerprint(cert), devicetls.CAPEMProperty: pemText}); err == nil || !strings.Contains(err.Error(), "two answers") {
		t.Errorf("a pin beside a pinned authority: %v", err)
	}
	pinned, err := parse(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: devicetls.Fingerprint(cert)})
	if err != nil {
		t.Fatal(err)
	}
	if w := pinned.Warnings("cml"); len(w) != 1 || !strings.Contains(w[0], `device "cml" trusts one pinned certificate`) {
		t.Errorf("warnings %q", w)
	}
	found := false
	for _, p := range devicetls.Properties() {
		found = found || p == devicetls.FingerprintProperty
	}
	if !found {
		t.Error("Properties() leaves out the pin, so no device type would dispatch it")
	}
}

// TestExplain_NamesTheCertificateForEachRefusal: with the server's own
// certificate pinned as the authority, an expired one and one dialed by a
// name it does not hold are each refused by Go, and Explain names the
// certificate either way.
func TestExplain_NamesTheCertificateForEachRefusal(t *testing.T) {
	for name, notAfter := range map[string]time.Time{
		"expired":    time.Now().Add(-30 * 24 * time.Hour),
		"wrong name": time.Now().Add(30 * 24 * time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			srv, cert := certServer(t, notAfter)
			pemText := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
			s, err := parse(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: pemText})
			if err != nil {
				t.Fatal(err)
			}
			err = get(t, s, srv)
			var untrusted *devicetls.UntrustedError
			if err == nil || !errors.As(devicetls.Explain(err), &untrusted) || untrusted.Fingerprint != devicetls.Fingerprint(cert) {
				t.Errorf("%v explained as %v", err, devicetls.Explain(err))
			}
		})
	}
}

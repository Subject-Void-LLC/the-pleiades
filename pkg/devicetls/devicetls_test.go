// Tests for reading a device's TLS settings, the warnings each weakening
// carries, and mutual TLS against a real server that requires a client
// certificate.
package devicetls_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func parse(props map[string]inventory.PropertyValue) (devicetls.Settings, error) {
	return devicetls.Parse(inventory.NewProperties(props))
}

// TestParse covers each setting's accepted forms and each refusal, the
// contradictions included: a flag that allows nothing is refused, since
// its author believes something is enabled that is not.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]inventory.PropertyValue
		want  uint16
		ok    bool
	}{
		{"defaults", nil, tls.VersionTLS12, true},
		{"1.3 as text", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.3"}, tls.VersionTLS13, true},
		{"1.2 as a YAML number", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: 1.2}, tls.VersionTLS12, true},
		{"1.0 as a YAML number, allowed", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: 1.0, devicetls.AllowDeprecatedProperty: true}, tls.VersionTLS10, true},
		{"1.1 allowed", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.1", devicetls.AllowDeprecatedProperty: true}, tls.VersionTLS11, true},
		{"1.0 without its flag", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0"}, 0, false},
		{"the flag with a 1.2 floor", map[string]inventory.PropertyValue{devicetls.AllowDeprecatedProperty: true}, 0, false},
		{"an unknown version", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.4"}, 0, false},
		{"SSL 3.0", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "0.3", devicetls.AllowDeprecatedProperty: true}, 0, false},
		{"a flag as text", map[string]inventory.PropertyValue{devicetls.AllowLegacyCiphersProperty: "yes"}, 0, false},
		{"a version of the wrong type", map[string]inventory.PropertyValue{devicetls.MinVersionProperty: true}, 0, false},
		{"a server name with a path", map[string]inventory.PropertyValue{devicetls.ServerNameProperty: "a.example/x"}, 0, false},
		{"a CA that is not PEM", map[string]inventory.PropertyValue{devicetls.CAPEMProperty: "not a certificate"}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parse(tc.props)
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
			if tc.ok && s.MinVersion != tc.want {
				t.Errorf("floor %s, want %s", tls.VersionName(s.MinVersion), tls.VersionName(tc.want))
			}
		})
	}
	if s := devicetls.For(struct{}{}); s.MinVersion != tls.VersionTLS12 || s.PinnedCA() {
		t.Errorf("a device with no settings got %+v", s)
	}
	var zero devicetls.Settings
	cfg, err := zero.Config(nil)
	if err != nil || cfg.MinVersion != tls.VersionTLS12 || zero.Deprecated() || len(zero.Warnings("d")) != 0 {
		t.Errorf("a zero Settings reads as a TLS 1.0 floor: %v, deprecated=%v", err, zero.Deprecated())
	}
}

// TestCAPEM_HandsBackThePinnedAuthorityAsWritten covers the accessor a
// client that takes PEM rather than a pool reads (pkg/winrmexec's does). The
// PEM must come back byte for byte, and a pool built from it alone must be
// enough to verify the real server it pins, or that client would connect to
// nothing or, worse, fall back to the system's roots. A device with no pin
// must answer nil, which such a client reads as "use the system's roots".
func TestCAPEM_HandsBackThePinnedAuthorityAsWritten(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	want := pinned(srv)

	s, err := parse(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: want})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(s.CAPEM()); got != want {
		t.Fatalf("CAPEM() = %q, want the pinned PEM as written", got)
	}

	// Verify the real server with a pool built from CAPEM() and nothing
	// else, the way a PEM-taking client builds its own.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(s.CAPEM()) {
		t.Fatal("CAPEM() did not parse as a certificate")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("a pool built from CAPEM() did not verify the pinned server: %v", err)
	}
	_ = resp.Body.Close()

	if unpinned, err := parse(nil); err != nil || unpinned.CAPEM() != nil {
		t.Errorf("a device with no pin: CAPEM() = %q, err %v; want nil", unpinned.CAPEM(), err)
	}
}

// TestWarnings: the defaults warn about nothing, and each weakening says
// what it is, naming its own flag.
func TestWarnings(t *testing.T) {
	if w := devicetls.For(nil).Warnings("d1"); len(w) != 0 {
		t.Errorf("the defaults warned: %v", w)
	}
	s, err := parse(map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true, devicetls.AllowLegacyCiphersProperty: true})
	if err != nil {
		t.Fatal(err)
	}
	w := s.Warnings("d1")
	if len(w) != 2 || !strings.Contains(w[0], devicetls.AllowDeprecatedProperty) || !strings.Contains(w[0], "RFC 8996") || !strings.Contains(w[1], devicetls.AllowLegacyCiphersProperty) {
		t.Errorf("warnings %q", w)
	}
}

// mtlsIdentity is a client certificate and key, and a server that
// requires a certificate that CA signed.
type mtlsIdentity struct {
	certPEM, keyPEM []byte
	pfxBase64       string
	server          *httptest.Server
}

func newMTLS(t *testing.T) mtlsIdentity {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "client CA"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "device client"},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafCert, _ := x509.ParseCertificate(leafDER)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	bundle, err := pkcs12.Modern.Encode(key, leafCert, nil, "bundle-pass")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return mtlsIdentity{
		certPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		keyPEM:    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		pfxBase64: base64.StdEncoding.EncodeToString(bundle),
		server:    srv,
	}
}

// TestMutualTLS presents the stored certificate, from a certificate and
// key or from a PKCS#12 bundle, to a server that requires one; without
// tls_client_certificate the same server refuses the connection.
func TestMutualTLS(t *testing.T) {
	id := newMTLS(t)
	get := func(props map[string]inventory.PropertyValue, secrets map[string]string) error {
		props[devicetls.CAPEMProperty] = pinned(id.server)
		s, err := parse(props)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := s.Config(secrets)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}).Get(id.server.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	withCert := map[string]inventory.PropertyValue{devicetls.ClientCertificateProperty: true}
	if err := get(map[string]inventory.PropertyValue{devicetls.ClientCertificateProperty: true}, map[string]string{wire.SecretCertificatePEM: string(id.certPEM), wire.SecretPrivateKeyPEM: string(id.keyPEM)}); err != nil {
		t.Errorf("certificate and key: %v", err)
	}
	if err := get(map[string]inventory.PropertyValue{devicetls.ClientCertificateProperty: true}, map[string]string{wire.SecretPFXBase64: id.pfxBase64, wire.SecretPassphrase: "bundle-pass"}); err != nil {
		t.Errorf("PKCS#12 bundle: %v", err)
	}
	if err := get(map[string]inventory.PropertyValue{}, map[string]string{wire.SecretCertificatePEM: string(id.certPEM), wire.SecretPrivateKeyPEM: string(id.keyPEM)}); err == nil {
		t.Error("the server accepted a client that presented nothing")
	}
	if err := get(withCert, nil); err == nil || !strings.Contains(err.Error(), "no client certificate") {
		t.Errorf("no stored certificate: %v", err)
	}
	both := map[string]string{wire.SecretPFXBase64: id.pfxBase64, wire.SecretCertificatePEM: string(id.certPEM)}
	if _, err := devicetls.ClientCertificate(both); err == nil {
		t.Error("a bundle beside a certificate was accepted")
	}
	if _, err := devicetls.ClientCertificate(map[string]string{wire.SecretCertificatePEM: string(id.certPEM), wire.SecretPrivateKeyPEM: "garbage"}); err == nil {
		t.Error("a certificate with an unusable key was accepted")
	}
	if _, err := devicetls.ClientCertificate(map[string]string{wire.SecretPFXBase64: id.pfxBase64, wire.SecretPassphrase: "wrong"}); err == nil {
		t.Error("a bundle opened with the wrong passphrase")
	}
}

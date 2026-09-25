// Tests for WithDeviceTLS and the options it sets, against a real TLS
// listener: a pinned authority and a server name either reach the
// handshake or they do not, and the handshake's own error says which.
package winrmexec

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// clientIdentity returns a self-signed client certificate and key as PEM.
func clientIdentity(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "lab"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// tlsListener starts a TLS server that answers every request with plain
// text, and returns where it is and its certificate as PEM. Getting that
// text back means the handshake succeeded; the certificate names
// example.com and 127.0.0.1, as httptest's does.
func tlsListener(t *testing.T) (Target, []byte) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not soap"))
	}))
	t.Cleanup(server.Close)
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	return Target{Host: host, Port: p}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
}

// deviceSettings parses TLS settings the way a device record does.
func deviceSettings(t *testing.T, props map[string]inventory.PropertyValue) devicetls.Settings {
	t.Helper()
	s, err := devicetls.Parse(inventory.NewProperties(props))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWithDeviceTLS_ReachesTheHandshake(t *testing.T) {
	target, caPEM := tlsListener(t)
	certPEM, keyPEM := clientIdentity(t)
	auth := Auth{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM}
	run := func(opts Options) error {
		opts.Timeout = 10 * time.Second
		_, err := Run(context.Background(), target, auth, ShellPowerShell, "hostname", opts)
		return err
	}

	// The system's roots do not know the listener's authority.
	if err := run(Options{}); err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Errorf("no pinned authority: err = %v, want an unknown-authority failure", err)
	}
	// The device's pinned authority gets the connection past TLS, to a
	// reply that is not SOAP.
	pinned := WithDeviceTLS(Options{}, deviceSettings(t, map[string]inventory.PropertyValue{devicetls.CAPEMProperty: string(caPEM)}))
	if err := run(pinned); err == nil || strings.Contains(err.Error(), "certificate") || !strings.Contains(err.Error(), "rather than SOAP") {
		t.Errorf("pinned authority: err = %v, want the handshake to succeed and the reply refused as not SOAP", err)
	}
	// A server name the certificate does not carry is checked, and fails.
	named := WithDeviceTLS(Options{}, deviceSettings(t, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: string(caPEM), devicetls.ServerNameProperty: "wrong.test"}))
	if err := run(named); err == nil || !strings.Contains(err.Error(), "wrong.test") {
		t.Errorf("wrong server name: err = %v, want a failure naming wrong.test", err)
	}
	// And one it does carry passes.
	right := WithDeviceTLS(Options{}, deviceSettings(t, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: string(caPEM), devicetls.ServerNameProperty: "example.com"}))
	if err := run(right); err == nil || !strings.Contains(err.Error(), "rather than SOAP") {
		t.Errorf("right server name: err = %v, want the handshake to succeed", err)
	}
}

func TestWithDeviceTLS_LeavesDefaultsAlone(t *testing.T) {
	opts := WithDeviceTLS(Options{CACert: []byte("keep"), ServerName: "keep"}, devicetls.Settings{})
	if string(opts.CACert) != "keep" || opts.ServerName != "keep" {
		t.Errorf("settings naming nothing changed opts: %+v", opts)
	}
}

// A pin over HTTP would be ignored, so a password credential, which
// always connects over HTTP, is refused with one before anything is sent.
func TestRun_RefusesAPinOverHTTP(t *testing.T) {
	target, caPEM := tlsListener(t)
	password := Auth{Username: "u", Password: "p"}
	for name, opts := range map[string]Options{
		"authority":   {CACert: caPEM},
		"server name": {ServerName: "example.com"},
	} {
		_, err := Run(context.Background(), target, password, ShellNone, "whoami", opts)
		if err == nil || !strings.Contains(err.Error(), "applies only over HTTPS") {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
}

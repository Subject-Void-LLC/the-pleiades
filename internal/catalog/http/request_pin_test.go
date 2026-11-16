// This file tests http.request's device mode against a real HTTPS server
// whose certificate is pinned, not chained: a key pin that survives the
// certificate issued again with the same key, a list of pins held through a
// rotation and refused once its old pin is retired, and a pin onboarding
// recorded on first use, trusted and said to be unconfirmed.
package http_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// pinnedServer is an HTTPS API on loopback presenting a self-signed
// certificate the test can change, counting the requests that reached it.
type pinnedServer struct {
	*httptest.Server
	cert atomic.Pointer[tls.Certificate]
	hits atomic.Int32
}

// selfSignedFor issues a self-signed certificate for 127.0.0.1 with key.
func selfSignedFor(t *testing.T, key *ecdsa.PrivateKey, serial int64) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "api.lab.invalid"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, cert
}

// newPinnedServer starts one presenting cert.
func newPinnedServer(t *testing.T, cert tls.Certificate) *pinnedServer {
	t.Helper()
	s := &pinnedServer{}
	s.cert.Store(&cert)
	s.Server = httptest.NewUnstartedServer(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { s.hits.Add(1) }))
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*s.cert.Load()}}, nil
	}}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// present makes s present cert from its next handshake.
func (s *pinnedServer) present(cert tls.Certificate) {
	s.cert.Store(&cert)
	s.CloseClientConnections()
}

// pinnedDevice builds an onboarded generic_http device for base trusting what
// tlsProps says.
func pinnedDevice(t *testing.T, base string, tlsProps map[string]inventory.PropertyValue) inventory.InventoryItem {
	t.Helper()
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: base, generic.HTTPAuthProperty: httpapi.AuthBearer}
	for k, v := range tlsProps {
		props[k] = v
	}
	d := inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
	d.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = d.Property()
	item, err := generic.NewHTTP(record.Record{ID: "api1", Name: "api1", Type: generic.TypeHTTP, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// call sends one http.request to dev's API, returning what the task recorded
// as warnings and its error.
func call(dev inventory.InventoryItem) ([]string, error) {
	rc := requestSecretsContext{newRequestContext(), map[string]string{"password": "tok"}}
	_, err := http.Request(context.Background(), rc, dev, map[string]any{"url": "/items"})
	w, _ := rc.stats[sdk.StatWarnings].([]string)
	return w, err
}

// TestRequestDevice_KeyPinAndRotation follows one device's certificate: a
// key pin reaches a certificate issued again with the same key, a list holds
// the old and new certificate through a rotation, and once the old pin is
// retired the old certificate is refused before any request carries the
// credential.
func TestRequestDevice_KeyPinAndRotation(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldTLS, oldCert := selfSignedFor(t, key, 1)
	againTLS, againCert := selfSignedFor(t, key, 2)
	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newTLS, newCert := selfSignedFor(t, newKey, 3)
	srv := newPinnedServer(t, againTLS)

	if _, err := call(pinnedDevice(t, srv.URL, map[string]inventory.PropertyValue{devicetls.FingerprintProperty: devicetls.KeyFingerprint(oldCert)})); err != nil {
		t.Errorf("a key pin refused a certificate issued again for its key: %v", err)
	}
	var untrusted *devicetls.UntrustedError
	if _, err := call(pinnedDevice(t, srv.URL, map[string]inventory.PropertyValue{devicetls.FingerprintProperty: devicetls.Fingerprint(oldCert)})); !errors.As(err, &untrusted) || untrusted.Fingerprint != devicetls.Fingerprint(againCert) {
		t.Errorf("a certificate pin of the old certificate: %v, want the new one refused by its fingerprint", err)
	}

	rotating := pinnedDevice(t, srv.URL, map[string]inventory.PropertyValue{devicetls.FingerprintProperty: devicetls.Fingerprint(oldCert) + "," + devicetls.Fingerprint(newCert)})
	for name, cert := range map[string]tls.Certificate{"old": oldTLS, "new": newTLS} {
		srv.present(cert)
		w, err := call(rotating)
		if err != nil {
			t.Errorf("during the rotation the %s certificate was refused: %v", name, err)
		}
		if !slices.ContainsFunc(w, func(s string) bool { return strings.Contains(s, "trusts 2 pins") }) {
			t.Errorf("warnings %q do not name both pins", w)
		}
	}

	retired := pinnedDevice(t, srv.URL, map[string]inventory.PropertyValue{devicetls.FingerprintProperty: []any{devicetls.Fingerprint(newCert)}})
	srv.present(oldTLS)
	before := srv.hits.Load()
	if _, err := call(retired); !errors.As(err, &untrusted) || untrusted.Fingerprint != devicetls.Fingerprint(oldCert) {
		t.Errorf("the old certificate after its pin was retired: %v, want it refused", err)
	}
	if srv.hits.Load() != before {
		t.Error("a refused certificate still received the request and its credential")
	}
}

// TestRequestDevice_AFirstUsePinIsTrustedAndSaidUnconfirmed: a device whose
// only trust is the pin onboarding recorded on first use is reached, and
// the task says nobody confirmed it.
func TestRequestDevice_AFirstUsePinIsTrustedAndSaidUnconfirmed(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certTLS, cert := selfSignedFor(t, key, 1)
	srv := newPinnedServer(t, certTLS)
	w, err := call(pinnedDevice(t, srv.URL, map[string]inventory.PropertyValue{devicetls.FirstUseProperty: devicetls.Fingerprint(cert)}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(w, func(s string) bool { return strings.Contains(s, "no person has confirmed") }) {
		t.Errorf("warnings %q do not say the first-use pin is unconfirmed", w)
	}
}

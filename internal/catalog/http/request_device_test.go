// This file tests http.request's device mode, a path on the target
// device's own API, against real HTTPS servers whose certificate is
// verified for real: TestMain makes a certificate authority of its own the
// process's only trusted root, through SSL_CERT_FILE, which is how an
// operator trusts a private CA too. httptest's built-in certificate stays
// untrusted, so the certificate tests in request_test.go are unaffected.
package http_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// requestDeviceCert is a certificate for 127.0.0.1 issued by the CA
// TestMain trusts.
var requestDeviceCert tls.Certificate

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "http-request-ca")
	if err != nil {
		panic(err)
	}
	caPEM, leaf, err := requestIssue()
	if err != nil {
		panic(err)
	}
	requestDeviceCert = leaf
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), caPEM, 0o600); err != nil {
		panic(err)
	}
	// Set before anything verifies a certificate: the system roots are
	// read once per process.
	_ = os.Setenv("SSL_CERT_FILE", filepath.Join(dir, "ca.pem"))
	_ = os.Setenv("SSL_CERT_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// requestIssue makes a CA and a leaf for 127.0.0.1 signed by it.
func requestIssue() ([]byte, tls.Certificate, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, tls.Certificate{}, err
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "http.request test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, tls.Certificate{}, err
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, tls.Certificate{}, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return caPEM, tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: key}, nil
}

// requestDeviceServer is a real HTTPS server with a trusted certificate,
// recording what reached it.
type requestDeviceServer struct {
	*httptest.Server
	hits atomic.Int32
	auth atomic.Value
	uri  atomic.Value
}

func newRequestDeviceServer(t *testing.T, h nethttp.HandlerFunc) *requestDeviceServer {
	t.Helper()
	s := &requestDeviceServer{}
	s.Server = httptest.NewUnstartedServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		s.hits.Add(1)
		s.auth.Store(r.Header.Get("Authorization"))
		s.uri.Store(r.URL.RequestURI())
		if h != nil {
			h(w, r)
		}
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{requestDeviceCert}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// requestAPIDevice builds an onboarded generic_http device for base.
func requestAPIDevice(t *testing.T, base, auth string, onboarded bool) inventory.InventoryItem {
	t.Helper()
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: base, generic.HTTPAuthProperty: auth}
	if onboarded {
		// Bound to the properties it was made against, as onboarding binds it.
		d := inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
		d.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
		props[inventory.DiscoveredProperty] = d.Property()
	}
	item, err := generic.NewHTTP(record.Record{ID: "api1", Name: "api1", Type: generic.TypeHTTP, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// requestRepointedDevice builds a generic_http device onboarded against
// another base URL and since pointed at base, as an inventory write that
// needs no onboarding leaves it: its discovery grants nothing.
func requestRepointedDevice(t *testing.T, base string) inventory.InventoryItem {
	t.Helper()
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://onboarded.invalid", generic.HTTPAuthProperty: httpapi.AuthBasic}
	d := inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
	d.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = d.Property()
	props[generic.BaseURLProperty] = base
	item, err := generic.NewHTTP(record.Record{ID: "api1", Name: "api1", Type: generic.TypeHTTP, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// requestSecretsContext is a requestContext holding the device's
// credential, as the engine's context does.
type requestSecretsContext struct {
	*requestContext
	secrets map[string]string
}

func (c requestSecretsContext) InjectSecrets() map[string]string { return c.secrets }

// TestRequestDevice_SendsTheDeviceCredential: a path on the device's API
// is joined to its base URL and carries the device's own credential.
func TestRequestDevice_SendsTheDeviceCredential(t *testing.T) {
	srv := newRequestDeviceServer(t, nil)
	dev := requestAPIDevice(t, srv.URL+"/api", httpapi.AuthBasic, true)
	rc := requestSecretsContext{newRequestContext(), map[string]string{"username": "api", "password": "api-secret"}}
	if _, err := http.Request(context.Background(), rc, dev, map[string]any{"url": "/items?page=2"}); err != nil {
		t.Fatal(err)
	}
	if got := srv.uri.Load(); got != "/api/items?page=2" {
		t.Errorf("the server was asked for %v", got)
	}
	if got, _ := srv.auth.Load().(string); !strings.HasPrefix(got, "Basic ") {
		t.Errorf("authorization %q, want the device's basic credential", got)
	}
}

// TestRequestDevice_FullURLCarriesNoCredential: the same device targeted
// with a full URL gets the old behavior, and no credential.
func TestRequestDevice_FullURLCarriesNoCredential(t *testing.T) {
	srv := newRequestDeviceServer(t, nil)
	dev := requestAPIDevice(t, srv.URL, httpapi.AuthBearer, true)
	rc := requestSecretsContext{newRequestContext(), map[string]string{"password": "tok"}}
	if _, err := http.Request(context.Background(), rc, dev, map[string]any{"url": srv.URL + "/items"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := srv.auth.Load().(string); got != "" {
		t.Errorf("a full URL carried authorization %q", got)
	}
}

// TestRequestDevice_RedirectLeavesTheCredentialBehind: a redirect to
// another origin is not followed, so the other server sees nothing, and
// the redirect is what the status check judges.
func TestRequestDevice_RedirectLeavesTheCredentialBehind(t *testing.T) {
	other := newRequestDeviceServer(t, nil)
	srv := newRequestDeviceServer(t, func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, other.URL+"/steal", nethttp.StatusFound)
	})
	dev := requestAPIDevice(t, srv.URL, httpapi.AuthBearer, true)
	rc := requestSecretsContext{newRequestContext(), map[string]string{"password": "tok"}}
	_, err := http.Request(context.Background(), rc, dev, map[string]any{"url": "/items", "status_code": 302})
	if err != nil {
		t.Fatal(err)
	}
	if other.hits.Load() != 0 {
		t.Errorf("the other origin received %d requests", other.hits.Load())
	}
}

// TestRequestDevice_Refusals: each is refused before any request is sent.
func TestRequestDevice_Refusals(t *testing.T) {
	srv := newRequestDeviceServer(t, nil)
	onboarded := requestAPIDevice(t, srv.URL, httpapi.AuthBasic, true)
	creds := map[string]string{"username": "api", "password": "api-secret"}
	for _, tc := range []struct {
		name   string
		device inventory.InventoryItem
		params map[string]any
		creds  map[string]string
		want   string
	}{
		{"no target device", nil, map[string]any{"url": "/items"}, creds, "no target device"},
		{"not onboarded", requestAPIDevice(t, srv.URL, httpapi.AuthBasic, false), map[string]any{"url": "/items"}, creds, "HTTPAPICapable"},
		{"climbs out", onboarded, map[string]any{"url": "/a/../../b"}, creds, ".."},
		{"protocol-relative", onboarded, map[string]any{"url": "//evil.invalid/x"}, creds, "one /"},
		{"own authorization", onboarded, map[string]any{"url": "/items", "headers": map[string]any{"authorization": "Bearer x"}}, creds, "authorization"},
		{"own host", onboarded, map[string]any{"url": "/items", "headers": map[string]any{"Host": "evil.invalid"}}, creds, "Host"},
		{"certificates off with a credential", onboarded, map[string]any{"url": "/items", "validate_certs": false}, creds, "validate_certs"},
		{"credential missing", onboarded, map[string]any{"url": "/items"}, nil, "stored username and password"},
		{"repointed since onboarding", requestRepointedDevice(t, srv.URL), map[string]any{"url": "/items"}, creds, "run `pleiades onboard api1` again"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := srv.hits.Load()
			rc := requestSecretsContext{newRequestContext(), tc.creds}
			_, err := http.Request(context.Background(), rc, tc.device, tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want one naming %q", err, tc.want)
			}
			if srv.hits.Load() != before {
				t.Errorf("a refused request reached the server (%d)", srv.hits.Load()-before)
			}
		})
	}
}

// badBaseDevice declares and implements HTTPAPICapable with a base URL
// that sends a credential over plain http, which the validation refuses
// even from a device type that did not check it.
type badBaseDevice struct{ inventory.InventoryItem }

func (badBaseDevice) HasCapability(capability.Name) bool  { return true }
func (badBaseDevice) Name() string                        { return "bad1" }
func (badBaseDevice) HTTPBaseURL() string                 { return "http://api.invalid" }
func (badBaseDevice) HTTPAuth() string                    { return httpapi.AuthBasic }
func (badBaseDevice) HTTPAllowPlaintextCredentials() bool { return false }

// TestRequestDevice_WhereTheBaseURLIsUnavailable: on the Walk tier a Runner
// rebuilds the device from its dispatch, which declares the capability and
// carries no base URL; the refusal says so rather than "onboard first". A
// device whose own base URL would leak its credential is refused too.
func TestRequestDevice_WhereTheBaseURLIsUnavailable(t *testing.T) {
	dispatched := external.NewDevice(wire.DispatchPayload{DeviceName: "api1", Capabilities: []capability.Name{capability.NameHTTPAPI}})
	_, err := http.Request(context.Background(), newRequestContext(), dispatched, map[string]any{"url": "/items"})
	if err == nil || !strings.Contains(err.Error(), "not available where this task runs") {
		t.Errorf("the dispatched device: %v", err)
	}
	if _, err := http.Request(context.Background(), newRequestContext(), badBaseDevice{}, map[string]any{"url": "/items"}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("a leaking base URL: %v", err)
	}
}

// TestRequestDevice_FollowsItsOwnRedirects: a redirect within the device's
// origin is followed with the credential, and a loop ends with an error.
func TestRequestDevice_FollowsItsOwnRedirects(t *testing.T) {
	srv := newRequestDeviceServer(t, func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/old":
			nethttp.Redirect(w, r, "/new", nethttp.StatusMovedPermanently)
		case "/loop":
			nethttp.Redirect(w, r, "/loop", nethttp.StatusFound)
		}
	})
	dev := requestAPIDevice(t, srv.URL, httpapi.AuthBearer, true)
	rc := requestSecretsContext{newRequestContext(), map[string]string{"password": "tok"}}
	if _, err := http.Request(context.Background(), rc, dev, map[string]any{"url": "/old"}); err != nil {
		t.Fatal(err)
	}
	if srv.uri.Load() != "/new" || srv.auth.Load() != "Bearer tok" {
		t.Errorf("the redirect reached %v with %v", srv.uri.Load(), srv.auth.Load())
	}
	if _, err := http.Request(context.Background(), rc, dev, map[string]any{"url": "/loop"}); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Errorf("a redirect loop returned %v", err)
	}
}

// tlsDeviceServer is an HTTPS device API with the trusted certificate,
// restricted to one version range.
func tlsDeviceServer(t *testing.T, minV, maxV uint16) *requestDeviceServer {
	t.Helper()
	s := &requestDeviceServer{}
	s.Server = httptest.NewUnstartedServer(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { s.hits.Add(1) }))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{requestDeviceCert}, MinVersion: minV, MaxVersion: maxV}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// requestDeviceWith builds an onboarded generic_http device for base with
// extra settings.
func requestDeviceWith(t *testing.T, base string, extra map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	t.Helper()
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: base}
	for k, v := range extra {
		props[k] = v
	}
	// Bound to the properties it was made against, extra included, as
	// onboarding binds it.
	d := inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
	d.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = d.Property()
	return generic.NewHTTP(record.Record{ID: "api1", Name: "api1", Type: generic.TypeHTTP, Properties: props})
}

// TestRequestDevice_NoDowngradeUnlessAllowed: a device API that speaks
// only TLS 1.0 is not reached by default, and with the record's flags it
// is, the run recording the warning.
func TestRequestDevice_NoDowngradeUnlessAllowed(t *testing.T) {
	srv := tlsDeviceServer(t, tls.VersionTLS10, tls.VersionTLS10)
	plain, err := requestDeviceWith(t, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := http.Request(context.Background(), newRequestContext(), plain, map[string]any{"url": "/items"}); err == nil || srv.hits.Load() != 0 {
		t.Fatalf("a default device reached a TLS 1.0 API: err %v, %d requests", err, srv.hits.Load())
	}
	allowed, err := requestDeviceWith(t, srv.URL, map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true})
	if err != nil {
		t.Fatal(err)
	}
	rc := newRequestContext()
	if _, err := http.Request(context.Background(), rc, allowed, map[string]any{"url": "/items"}); err != nil {
		t.Fatal(err)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "RFC 8996") {
		t.Errorf("warnings %q", warnings)
	}
}

// TestRequestDevice_PlaintextCredentialOnlyWhenAllowed: a device with an
// http:// base URL and a credential mode is refused without the flag; with
// it the credential is sent and the run records the rotation warning.
func TestRequestDevice_PlaintextCredentialOnlyWhenAllowed(t *testing.T) {
	var sent string
	srv := httptest.NewServer(nethttp.HandlerFunc(func(_ nethttp.ResponseWriter, r *nethttp.Request) { sent = r.Header.Get("Authorization") }))
	defer srv.Close()
	if _, err := requestDeviceWith(t, srv.URL, map[string]inventory.PropertyValue{generic.HTTPAuthProperty: httpapi.AuthBearer}); err == nil {
		t.Fatal("a plain-HTTP credential was accepted without its flag")
	}
	dev, err := requestDeviceWith(t, srv.URL, map[string]inventory.PropertyValue{generic.HTTPAuthProperty: httpapi.AuthBearer, httpapi.AllowPlaintextCredentialsProperty: true})
	if err != nil {
		t.Fatal(err)
	}
	rc := requestSecretsContext{newRequestContext(), map[string]string{"password": "tok"}}
	if _, err := http.Request(context.Background(), rc, dev, map[string]any{"url": "/items"}); err != nil {
		t.Fatal(err)
	}
	if sent != "Bearer tok" {
		t.Errorf("the device saw authorization %q", sent)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "rotate the credential") {
		t.Errorf("warnings %q", warnings)
	}
}

// TestRequestDevice_TLSRefusalsSendNothing: a device whose record asks for
// mutual TLS with no stored certificate is refused before a byte is sent,
// and so is a call whose warning cannot be recorded, since a weakened call
// that could not say so must not run.
func TestRequestDevice_TLSRefusalsSendNothing(t *testing.T) {
	srv := tlsDeviceServer(t, tls.VersionTLS12, 0)
	mtls, err := requestDeviceWith(t, srv.URL, map[string]inventory.PropertyValue{devicetls.ClientCertificateProperty: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := http.Request(context.Background(), newRequestContext(), mtls, map[string]any{"url": "/items"}); err == nil || !strings.Contains(err.Error(), "no client certificate") {
		t.Errorf("mutual TLS with no stored certificate: %v", err)
	}

	weakened, err := requestDeviceWith(t, srv.URL, map[string]inventory.PropertyValue{devicetls.AllowLegacyCiphersProperty: true})
	if err != nil {
		t.Fatal(err)
	}
	rc := newRequestContext()
	rc.failOn = sdk.StatWarnings
	if _, err := http.Request(context.Background(), rc, weakened, map[string]any{"url": "/items"}); err == nil {
		t.Error("a call whose warning could not be recorded ran")
	}
	if n := srv.hits.Load(); n != 0 {
		t.Errorf("the device received %d requests", n)
	}
}

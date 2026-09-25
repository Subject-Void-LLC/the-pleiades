// Tests that a device's TLS never goes below what its record allows, by
// real handshakes against in-process servers that each speak only one old
// thing: a deprecated version, a legacy cipher suite, or both. Each client
// setting must reach exactly the servers it allows and no other, and
// against a modern server every setting must still negotiate TLS 1.3.
package devicetls_test

import (
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// legacyServer is an HTTPS server restricted to cfg's versions and suites.
// httptest's certificate is RSA, which the RSA key exchange suites need.
func legacyServer(t *testing.T, minV, maxV uint16, suites []uint16) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: minV, MaxVersion: maxV, CipherSuites: suites}
	srv.Config.ErrorLog = nil
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// pinned returns srv's certificate as the PEM a tls_ca_pem property holds.
func pinned(srv *httptest.Server) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}

// handshake connects to srv with the settings props describe and returns
// the negotiated version, or the error.
func handshake(t *testing.T, srv *httptest.Server, props map[string]inventory.PropertyValue) (uint16, error) {
	t.Helper()
	withCA := map[string]inventory.PropertyValue{devicetls.CAPEMProperty: pinned(srv)}
	for k, v := range props {
		withCA[k] = v
	}
	settings, err := devicetls.Parse(inventory.NewProperties(withCA))
	if err != nil {
		t.Fatalf("settings %v: %v", props, err)
	}
	cfg, err := settings.Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.TLS.Version, nil
}

// TestNoDowngradeUnlessAllowed is the matrix: which client settings reach
// which old server, and the version a modern server negotiates.
func TestNoDowngradeUnlessAllowed(t *testing.T) {
	servers := map[string]*httptest.Server{
		"TLS 1.0 only":           legacyServer(t, tls.VersionTLS10, tls.VersionTLS10, nil),
		"TLS 1.1 only":           legacyServer(t, tls.VersionTLS11, tls.VersionTLS11, nil),
		"3DES only":              legacyServer(t, tls.VersionTLS12, tls.VersionTLS12, []uint16{tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA}),
		"RSA key exchange only":  legacyServer(t, tls.VersionTLS12, tls.VersionTLS12, []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256}),
		"TLS 1.0 and 3DES only":  legacyServer(t, tls.VersionTLS10, tls.VersionTLS10, []uint16{tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA}),
		"TLS 1.2 modern suites":  legacyServer(t, tls.VersionTLS12, tls.VersionTLS12, nil),
		"modern (TLS 1.3 first)": legacyServer(t, tls.VersionTLS12, tls.VersionTLS13, nil),
	}
	deprecated := map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true}
	legacy := map[string]inventory.PropertyValue{devicetls.AllowLegacyCiphersProperty: true}
	both := map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true, devicetls.AllowLegacyCiphersProperty: true}
	only13 := map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.3"}

	for _, tc := range []struct {
		client  string
		props   map[string]inventory.PropertyValue
		reaches map[string]bool
	}{
		{"default", nil, map[string]bool{"TLS 1.2 modern suites": true, "modern (TLS 1.3 first)": true}},
		{"deprecated versions allowed", deprecated, map[string]bool{"TLS 1.0 only": true, "TLS 1.1 only": true, "TLS 1.2 modern suites": true, "modern (TLS 1.3 first)": true}},
		{"legacy ciphers allowed", legacy, map[string]bool{"3DES only": true, "RSA key exchange only": true, "TLS 1.2 modern suites": true, "modern (TLS 1.3 first)": true}},
		{"both allowed", both, map[string]bool{"TLS 1.0 only": true, "TLS 1.1 only": true, "3DES only": true, "RSA key exchange only": true, "TLS 1.0 and 3DES only": true, "TLS 1.2 modern suites": true, "modern (TLS 1.3 first)": true}},
		{"TLS 1.3 floor", only13, map[string]bool{"modern (TLS 1.3 first)": true}},
	} {
		for name, srv := range servers {
			t.Run(tc.client+" vs "+name, func(t *testing.T) {
				version, err := handshake(t, srv, tc.props)
				if reached := err == nil; reached != tc.reaches[name] {
					t.Fatalf("reached=%v (err %v), want %v", reached, err, tc.reaches[name])
				}
				if name == "modern (TLS 1.3 first)" && version != tls.VersionTLS13 {
					t.Errorf("a modern server negotiated %s: a weakening lowered what it could have had", tls.VersionName(version))
				}
			})
		}
	}
}

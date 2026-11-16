// Tests for reading a device's TLS alert about this side's client
// certificate as a refusal, against real servers that require one.
package devicetls_test

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// denyingServer requires a client certificate and names no authority it
// accepts, so a client presents whatever it holds, and refuses every one it
// is presented, as a device whose access list denies a valid certificate.
func denyingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAnyClientCert,
		VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error { return errors.New("this certificate is denied") }}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// TestWatch_AClientCertificateTheDeviceRefuses connects to servers that
// require a client certificate, in TLS 1.3 and in TLS 1.2: with none stored,
// with one an authority the server does not trust signed, and with one a
// server that names no authority is presented and denies. Each failure is a
// refusal that says which, names tls_client_certificate and reports Refused.
func TestWatch_AClientCertificateTheDeviceRefuses(t *testing.T) {
	id, stranger := newMTLS(t), newMTLS(t)
	deny := denyingServer(t)
	held := map[string]string{wire.SecretCertificatePEM: string(stranger.certPEM), wire.SecretPrivateKeyPEM: string(stranger.keyPEM)}
	withCert := func() map[string]inventory.PropertyValue {
		return map[string]inventory.PropertyValue{devicetls.ClientCertificateProperty: true}
	}
	for _, tc := range []struct {
		name                string
		server              *httptest.Server
		props               map[string]inventory.PropertyValue
		secrets             map[string]string
		presented, withheld bool
	}{
		{"none stored", id.server, map[string]inventory.PropertyValue{}, nil, false, false},
		// newMTLS names every authority "client CA", and Go matches a request's
		// authorities by name, so this one is presented, and refused as
		// unknown_ca. pkg/gnmi's tests hold one under another name withheld.
		{"one from another authority of the same name", id.server, withCert(), held, true, false},
		{"one presented and refused", deny, withCert(), held, true, false},
	} {
		for _, maxVersion := range []uint16{tls.VersionTLS13, tls.VersionTLS12} {
			t.Run(fmt.Sprintf("%s, %s", tc.name, tls.VersionName(maxVersion)), func(t *testing.T) {
				props := map[string]inventory.PropertyValue{devicetls.CAPEMProperty: pinned(tc.server)}
				for k, v := range tc.props {
					props[k] = v
				}
				s, err := parse(props)
				if err != nil {
					t.Fatal(err)
				}
				cfg, err := s.Config(tc.secrets)
				if err != nil {
					t.Fatal(err)
				}
				cfg.MaxVersion = maxVersion
				watchedCfg, watch := devicetls.Watched(cfg)
				resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: watchedCfg}}).Get(tc.server.URL)
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("the server accepted the client")
				}
				if !watch.Asked() {
					t.Error("the watch did not see the server ask for a certificate")
				}
				explained := watch.Explain(err)
				var refused *devicetls.ClientCertificateRefusedError
				if !errors.As(explained, &refused) || !refused.Refused() || refused.Presented != tc.presented || refused.Withheld != tc.withheld {
					t.Fatalf("Explain(%v) = %+v, want Presented %v, Withheld %v", err, explained, tc.presented, tc.withheld)
				}
				if text := explained.Error(); !strings.Contains(text, devicetls.ClientCertificateProperty) || !strings.Contains(text, "no password or token") || !strings.Contains(text, refused.Alert) {
					t.Errorf("refusal = %q", text)
				}
				if !errors.Is(explained, err) {
					t.Error("the refusal does not unwrap to the failure it explains")
				}
			})
		}
	}
}

// TestWatch_LeavesEverythingElse holds a Watch to explaining only the
// device's own alert about this side's certificate: a plain error, an alert
// this side sent, and a handshake failure from a server that never asked for
// a certificate are each returned as they are, and an untrusted device
// certificate is still the device's.
func TestWatch_LeavesEverythingElse(t *testing.T) {
	cfg, watch := devicetls.Watched(nil)
	if cfg != nil || watch.Asked() {
		t.Fatal("a nil configuration was watched")
	}
	plain := errors.New("connection refused")
	if got := watch.Explain(plain); got != plain {
		t.Errorf("Explain(plain) = %v", got)
	}
	// An alert this side sent wraps tls.AlertError in Go's own handshake
	// error; it is never the device's refusal of this side's certificate.
	local := fmt.Errorf("tls: failed to verify certificate%w", tls.AlertError(42))
	if _, ok := watch.Refusal(local); ok {
		t.Error("an alert this side sent was read as the device's")
	}
	// handshake_failure is no refusal of a certificate nobody asked for.
	other := &net.OpError{Op: "remote error", Err: errors.New(tls.AlertError(40).Error())}
	if got := watch.Explain(other); got != error(other) {
		t.Errorf("Explain(handshake failure, never asked) = %v", got)
	}
	// The device's certificate refused by this side is said as that.
	id := newMTLS(t)
	_, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}).Get(id.server.URL)
	var untrusted *devicetls.UntrustedError
	if !errors.As(watch.Explain(err), &untrusted) {
		t.Errorf("Explain(%v) is not an UntrustedError", err)
	}
}

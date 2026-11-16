// Tests for reading a device's refusal of this side's client certificate,
// against real servers that ask for one: refusing in the handshake (TLS 1.2)
// or after it (TLS 1.3), at once or late, losing the alert to a reset, or
// accepting and sending nothing.
package devicetls_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls/devicetlstest"
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

// watchedGet makes one GET of rawURL with the TLS settings props and secrets
// make, through a Watch, and returns the failure as the Watch explains it.
func watchedGet(t *testing.T, rawURL string, props map[string]inventory.PropertyValue, secrets map[string]string, maxVersion uint16) (error, *devicetls.Watch) {
	t.Helper()
	s, err := parse(props)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Config(secrets)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxVersion = maxVersion
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	watchedCfg, watch := devicetls.Watched(cfg, devicetls.HTTPSAddress(u), devicetls.HTTPNextProtos...)
	resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: watchedCfg, ForceAttemptHTTP2: true}}).Get(rawURL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the server accepted the client")
	}
	return watch.Explain(context.Background(), err), watch
}

// TestWatch_AClientCertificateTheDeviceRefuses connects to servers that
// require a client certificate, in TLS 1.3 and in TLS 1.2: with none stored,
// with one an authority the server does not trust signed, and with one a
// server that names no authority is presented and denies. Each failure is a
// refusal that says which, names tls_client_certificate, says no request was
// processed and reports Refused.
func TestWatch_AClientCertificateTheDeviceRefuses(t *testing.T) {
	id, stranger := newMTLS(t), newMTLS(t)
	deny := denyingServer(t)
	held := map[string]string{wire.SecretCertificatePEM: string(stranger.certPEM), wire.SecretPrivateKeyPEM: string(stranger.keyPEM)}
	for _, tc := range []struct {
		name      string
		server    *httptest.Server
		cert      bool
		secrets   map[string]string
		presented bool
	}{
		{"none stored", id.server, false, nil, false},
		// newMTLS names every authority "client CA", and Go matches a request's
		// authorities by name, so this one is presented, and refused as
		// unknown_ca. pkg/gnmi's tests hold one under another name withheld.
		{"one from another authority of the same name", id.server, true, held, true},
		{"one presented and refused", deny, true, held, true},
	} {
		for _, maxVersion := range []uint16{tls.VersionTLS13, tls.VersionTLS12} {
			t.Run(fmt.Sprintf("%s, %s", tc.name, tls.VersionName(maxVersion)), func(t *testing.T) {
				props := map[string]inventory.PropertyValue{devicetls.CAPEMProperty: pinned(tc.server)}
				if tc.cert {
					props[devicetls.ClientCertificateProperty] = true
				}
				explained, watch := watchedGet(t, tc.server.URL, props, tc.secrets, maxVersion)
				var refused *devicetls.ClientCertificateRefusedError
				if !errors.As(explained, &refused) || !refused.Refused() || refused.Presented != tc.presented || refused.Withheld {
					t.Fatalf("Explain = %+v, want Presented %v", explained, tc.presented)
				}
				if !watch.Asked() {
					t.Error("the watch did not see the server ask for a certificate")
				}
				if text := explained.Error(); !strings.Contains(text, devicetls.ClientCertificateProperty) || !strings.Contains(text, "processed no request") || !strings.Contains(text, refused.Alert) {
					t.Errorf("refusal = %q", text)
				}
			})
		}
	}
}

// TestWatch_ARefusalTheRequestNeverSawIsDiagnosed refuses through servers
// whose alert the request's own connection does not read: one that resets its
// first connection with no alert, as a host that discards queued data on a
// reset loses it, and one whose alert comes late. The diagnosis, on a
// connection of its own that writes nothing, reads the refusal, and the
// server is dialed once for the request and once for the diagnosis.
func TestWatch_ARefusalTheRequestNeverSawIsDiagnosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts devicetlstest.Options
	}{
		{"an alert lost to a reset", devicetlstest.Options{Mode: devicetlstest.LoseFirst}},
		{"a late alert", devicetlstest.Options{Delay: 300 * time.Millisecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := devicetlstest.New(t, tc.opts)
			explained, _ := watchedGet(t, s.URL(), map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.CAPEM()}, nil, tls.VersionTLS13)
			var refused *devicetls.ClientCertificateRefusedError
			if !errors.As(explained, &refused) || refused.Presented {
				t.Fatalf("Explain = %v, want the refusal the diagnosis reads", explained)
			}
			if n := s.Handshakes(); n != 2 {
				t.Errorf("%d handshakes, want the request's and the diagnosis's", n)
			}
		})
	}
}

// TestWatch_ADiagnosisEndsAtTheCallersCancellation diagnoses a server that
// accepts the certificate and sends nothing, as an HTTP/1.1 server waits for
// its request, with the diagnosis's own bound far off: it ends when the
// caller's context does, reading no refusal, rather than holding the socket.
func TestWatch_ADiagnosisEndsAtTheCallersCancellation(t *testing.T) {
	devicetls.SetDiagnoseTimeout(t, time.Minute)
	s := devicetlstest.New(t, devicetlstest.Options{Mode: devicetlstest.Silent})
	settings, err := parse(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.CAPEM()})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, watch := devicetls.Watched(cfg, s.Addr(), devicetls.HTTPNextProtos...)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	started := time.Now()
	if refused, ok := watch.Diagnose(ctx, nil); ok {
		t.Fatalf("Diagnose of a silent server = %v", refused)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the diagnosis took %s after its caller gave up at 200ms", elapsed)
	}
	if s.Handshakes() != 1 {
		t.Errorf("%d handshakes, want one", s.Handshakes())
	}
}

// TestWatch_LeavesEverythingElse holds a Watch to its own refusals: a
// failure before any server asked for a certificate is returned as it is,
// and an untrusted device certificate is still the device's.
func TestWatch_LeavesEverythingElse(t *testing.T) {
	id := newMTLS(t)
	u, _ := url.Parse(id.server.URL)
	cfg, watch := devicetls.Watched(&tls.Config{MinVersion: tls.VersionTLS12}, devicetls.HTTPSAddress(u))
	_, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}).Get(id.server.URL)
	var untrusted *devicetls.UntrustedError
	if !errors.As(watch.Explain(context.Background(), err), &untrusted) {
		t.Errorf("Explain(%v) is not an UntrustedError", err)
	}
	plain := errors.New("connection refused")
	if got := watch.Explain(context.Background(), plain); got != plain {
		t.Errorf("Explain(plain) = %v", got)
	}
	if got := devicetls.HTTPSAddress(&url.URL{Scheme: "https", Host: "[::1]"}); got != "[::1]:443" {
		t.Errorf("HTTPSAddress = %s", got)
	}
}

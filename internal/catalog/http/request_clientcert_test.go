// Tests that a device API requiring a client certificate, presented none,
// refuses the call as a refusal naming tls_client_certificate, in TLS 1.3
// (the alert after the client's side of the handshake, so it meets the
// request) and in TLS 1.2 (inside the handshake).
package http_test

import (
	"context"
	"crypto/tls"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// TestRequestDevice_ARequiredClientCertificateIsARefusal calls a device API
// that requires a client certificate from a device presenting none, and
// holds the failure to a ClientCertificateRefusedError, which the engine
// stops an until on, with nothing read by the server.
func TestRequestDevice_ARequiredClientCertificateIsARefusal(t *testing.T) {
	for _, version := range []struct {
		name string
		max  uint16
	}{{"tls 1.3", 0}, {"tls 1.2", tls.VersionTLS12}} {
		t.Run(version.name, func(t *testing.T) {
			s := &requestDeviceServer{}
			s.Server = httptest.NewUnstartedServer(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { s.hits.Add(1) }))
			s.TLS = &tls.Config{Certificates: []tls.Certificate{requestDeviceCert}, MinVersion: tls.VersionTLS12, MaxVersion: version.max, ClientAuth: tls.RequireAnyClientCert}
			s.StartTLS()
			t.Cleanup(s.Close)
			dev, err := requestDeviceWith(t, s.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = http.Request(context.Background(), newRequestContext(), dev, map[string]any{"url": "/items"})
			var refused *devicetls.ClientCertificateRefusedError
			if !errors.As(err, &refused) || !refused.Refused() || refused.Presented {
				t.Fatalf("Request = %v, want a refusal of no client certificate", err)
			}
			if !strings.Contains(err.Error(), devicetls.ClientCertificateProperty) {
				t.Errorf("Request = %v, want it to name %s", err, devicetls.ClientCertificateProperty)
			}
			if n := s.hits.Load(); n != 0 {
				t.Errorf("the device read %d requests through a refused handshake", n)
			}
		})
	}
}

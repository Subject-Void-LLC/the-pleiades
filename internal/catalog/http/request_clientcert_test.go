// Tests that a device API requiring a client certificate, presented none,
// refuses the call as a refusal naming tls_client_certificate, in TLS 1.3
// (the alert after the client's side of the handshake, so it meets the
// request), in TLS 1.2 (inside the handshake), and when the request's own
// connection loses the alert to a reset.
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls/devicetlstest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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

// TestRequestDevice_AnAlertLostToAResetIsDiagnosed calls through a server
// that resets the request's connection with no alert: the refusal is read on
// a connection of its own, which carries no request.
func TestRequestDevice_AnAlertLostToAResetIsDiagnosed(t *testing.T) {
	s := devicetlstest.New(t, devicetlstest.Options{Mode: devicetlstest.LoseFirst})
	dev, err := requestDeviceWith(t, s.URL(), map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.CAPEM()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = http.Request(context.Background(), newRequestContext(), dev, map[string]any{"url": "/items"})
	var refused *devicetls.ClientCertificateRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Request = %v, want the refusal a diagnosis reads", err)
	}
	if n := s.Handshakes(); n != 2 {
		t.Errorf("%d handshakes, want the request's and the diagnosis's", n)
	}
}

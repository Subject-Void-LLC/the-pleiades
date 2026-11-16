// Package restconf_test: a RESTCONF server that requires a client
// certificate refusing a device that presents none, said as a refusal naming
// tls_client_certificate rather than as a transport failure, even when the
// request's own connection loses the alert to a reset.
package restconf_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls/devicetlstest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestClientCertificate_ARequiredOneIsARefusal discovers a server requiring
// a client certificate (TLS 1.3, so the alert comes after the client's side
// of the handshake and meets the request) and holds the failure to a
// ClientCertificateRefusedError with nothing read by the server.
func TestClientCertificate_ARequiredOneIsARefusal(t *testing.T) {
	opts := login
	opts.RequireClientCertificate = true
	s := restconftest.New(t, opts)
	_, _, err := discover(t, s, restconftest.NewDevice(t, s))
	var refused *devicetls.ClientCertificateRefusedError
	if !errors.As(err, &refused) || !refused.Refused() || refused.Presented {
		t.Fatalf("Discover = %v, want a refusal of no client certificate", err)
	}
	if !strings.Contains(err.Error(), devicetls.ClientCertificateProperty) {
		t.Errorf("Discover = %v, want it to name %s", err, devicetls.ClientCertificateProperty)
	}
	if paths := s.Paths(); len(paths) != 0 || s.Logins() != 0 {
		t.Errorf("the server read %v (%d logins) through a refused handshake", paths, s.Logins())
	}
}

// TestClientCertificate_AnAlertLostToAResetIsDiagnosed discovers through a
// server that resets the request's connection with no alert, as a host that
// discards queued data on a reset loses it: the refusal is read on a
// connection of its own.
func TestClientCertificate_AnAlertLostToAResetIsDiagnosed(t *testing.T) {
	s := devicetlstest.New(t, devicetlstest.Options{Mode: devicetlstest.LoseFirst})
	client, _, err := restconf.ClientFor(restconftest.NewDeviceAt(t, s.URL(), s.CAPEM()), "router", map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.Discover(context.Background())
	var refused *devicetls.ClientCertificateRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Discover = %v, want the refusal a diagnosis reads", err)
	}
}

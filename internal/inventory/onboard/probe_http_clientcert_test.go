// Onboarding a generic_http device whose server refuses the client
// certificate, or its absence: a refusal naming tls_client_certificate, even
// when the probe's own connection loses the alert to a reset.
package onboard

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls/devicetlstest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestHTTPProbe_ARefusedClientCertificateIsARefusal probes servers that
// refuse the connection over the client certificate, at once and with the
// alert lost to a reset, and holds the probe to the refusal.
func TestHTTPProbe_ARefusedClientCertificateIsARefusal(t *testing.T) {
	for name, opts := range map[string]devicetlstest.Options{
		"refused":                  {Mode: devicetlstest.Refuse},
		"an alert lost to a reset": {Mode: devicetlstest.LoseFirst},
	} {
		t.Run(name, func(t *testing.T) {
			s := devicetlstest.New(t, opts)
			dev := build(t, generic.TypeHTTP, map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.CAPEM(), generic.BaseURLProperty: s.URL()})
			_, err := httpProber{}.Probe(context.Background(), dev, nil)
			var refused *devicetls.ClientCertificateRefusedError
			if !errors.As(err, &refused) || !refused.Refused() {
				t.Fatalf("Probe = %v, want a refused client certificate", err)
			}
		})
	}
}

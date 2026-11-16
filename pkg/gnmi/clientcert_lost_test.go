// Package gnmi_test: a refused client certificate whose alert the call's own
// connection never reads (lost to a reset, as on a host that discards queued
// data on one, or sent late), and a server that accepts the certificate and
// sends nothing, each against pkg/devicetls/devicetlstest.
package gnmi_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls/devicetlstest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// clientAt is a client of s, trusting its certificate and sending no
// credential, closed when t ends.
func clientAt(t *testing.T, s *devicetlstest.Server) *gnmi.Client {
	t.Helper()
	settings, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.CAPEM()}))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := gnmi.New(gnmi.Options{Target: s.Addr(), TLS: cfg, Auth: gnmi.AuthNone})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestClientCertificate_ARefusalTheCallNeverSawIsStillOne refuses through a
// server that resets its first connection with no alert and one whose alert
// comes late, and holds Capabilities and Set alike to the refusal a
// diagnosis reads, a Set never to a lost answer.
func TestClientCertificate_ARefusalTheCallNeverSawIsStillOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts devicetlstest.Options
	}{
		{"an alert lost to a reset", devicetlstest.Options{Mode: devicetlstest.LoseFirst}},
		{"a late alert", devicetlstest.Options{Delay: 300 * time.Millisecond}},
	} {
		calls := map[string]func(*gnmi.Client) error{
			"Capabilities": func(c *gnmi.Client) error { _, err := c.Capabilities(ctxOf(t)); return err },
			"Set": func(c *gnmi.Client) error {
				_, err := c.Set(ctxOf(t), gnmi.SetRequest{Delete: []gnmi.Path{mustPath(t, "/a")}})
				return err
			},
		}
		for name, call := range calls {
			t.Run(tc.name+", "+name, func(t *testing.T) {
				err := call(clientAt(t, devicetlstest.New(t, tc.opts)))
				var refused *devicetls.ClientCertificateRefusedError
				if !errors.As(err, &refused) {
					t.Fatalf("%s = %v, want the refusal a diagnosis reads", name, err)
				}
				var lost *gnmi.AnswerLostError
				if errors.As(err, &lost) {
					t.Errorf("%s = %v, a lost answer for a call the server never processed", name, err)
				}
			})
		}
	}
}

// TestClientCertificate_ASilentServerIsNoRefusal calls a server that accepts
// the certificate and never answers: the call ends at its deadline, and
// nothing reads that as a refused certificate.
func TestClientCertificate_ASilentServerIsNoRefusal(t *testing.T) {
	c := clientAt(t, devicetlstest.New(t, devicetlstest.Options{Mode: devicetlstest.Silent}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := c.Capabilities(ctx)
	var refused *devicetls.ClientCertificateRefusedError
	if err == nil || errors.As(err, &refused) {
		t.Fatalf("Capabilities of a silent server = %v, want its deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("the call took %s past a 2s deadline", elapsed)
	}
}

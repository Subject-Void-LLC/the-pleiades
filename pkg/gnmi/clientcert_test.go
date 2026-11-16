// Package gnmi_test: a server that requires a client certificate refusing a
// client that presents none, or one it does not trust, as SONiC's gNMI server
// does with client_auth on (measured 2026-11-16): a refusal naming the
// device's client-certificate setting, never a server that is down.
package gnmi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// refusal is what every refused call must carry: the typed error, which
// the engine's Refused stops on.
type refusal interface {
	error
	Refused() bool
}

// TestClientCertificate_ARefusalIsSaidAsOne connects to servers requiring a
// client certificate, in TLS 1.3 (the alert after the client's side of the
// handshake, as SONiC's) and in TLS 1.2 (inside the handshake), with none
// stored and with one another authority signed stored, and holds every call
// to a ClientCertificateRefusedError that names tls_client_certificate, says
// no password reached the server, and is never a lost Set.
func TestClientCertificate_ARefusalIsSaidAsOne(t *testing.T) {
	for _, version := range []struct {
		name  string
		tls12 bool
	}{{"tls 1.3", false}, {"tls 1.2", true}} {
		s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword, ClientCertificate: true, TLS12: version.tls12})
		login := map[string]string{wire.SecretUsername: testUser, wire.SecretPassword: testPassword}
		stranger := map[string]string{}
		for k, v := range login {
			stranger[k] = v
		}
		for k, v := range s.UntrustedClientSecrets() {
			stranger[k] = v
		}
		// The server names the authority it accepts, and Go's client presents
		// a stored certificate only when its issuer is one, so the untrusted
		// certificate is withheld and the refusal says so.
		for _, tc := range []struct {
			name     string
			props    map[string]inventory.PropertyValue
			secrets  map[string]string
			withheld bool
		}{
			{"none presented", map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.AuthorityPEM()}, login, false},
			{"an untrusted one", map[string]inventory.PropertyValue{devicetls.CAPEMProperty: s.AuthorityPEM(), devicetls.ClientCertificateProperty: true}, stranger, true},
		} {
			t.Run(version.name+", "+tc.name, func(t *testing.T) {
				device := gnmitest.NewDeviceWith(t, s, tc.props)
				c, _, err := gnmi.ClientFor(device, "switch1", tc.secrets)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = c.Close() }()
				ctx := ctxOf(t)
				calls := map[string]func() error{
					"Capabilities": func() error { _, err := c.Capabilities(ctx); return err },
					"Get":          func() error { _, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/a")}}); return err },
					"Set": func() error {
						_, err := c.Set(ctx, gnmi.SetRequest{Update: []gnmi.SetValue{{Path: mustPath(t, "/a"), JSON: []byte(`1`)}}})
						return err
					},
					"Subscribe": func() error {
						_, err := c.SubscribeOnce(ctx, gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/a")}})
						return err
					},
				}
				for name, call := range calls {
					err := call()
					var refused *devicetls.ClientCertificateRefusedError
					if !errors.As(err, &refused) {
						t.Fatalf("%s = %v, want a ClientCertificateRefusedError", name, err)
					}
					var r refusal
					if !errors.As(err, &r) || !r.Refused() {
						t.Errorf("%s = %v, which does not report Refused", name, err)
					}
					if refused.Presented || refused.Withheld != tc.withheld {
						t.Errorf("%s: Presented %v, Withheld %v, want Withheld %v", name, refused.Presented, refused.Withheld, tc.withheld)
					}
					var lost *gnmi.AnswerLostError
					if errors.As(err, &lost) {
						t.Errorf("%s = %v, a lost answer for a call the server never read", name, err)
					}
					if text := err.Error(); !strings.Contains(text, devicetls.ClientCertificateProperty) || !strings.Contains(text, "no password or token") {
						t.Errorf("%s = %q, want it to name %s and say no password reached the server", name, text, devicetls.ClientCertificateProperty)
					}
				}
				if rpcs := s.RPCs(); len(rpcs) != 0 || s.Sets() != 0 {
					t.Errorf("the server answered %v (%d Sets) through a refused client certificate", rpcs, s.Sets())
				}
			})
		}
	}
}

// TestClientCertificate_AnUntrustedServerIsStillTheServers holds an alert
// this side sends over the server's certificate apart from the server's
// alert about this side's: the client's own refusal wraps the alert it sent,
// and must stay an UntrustedError.
func TestClientCertificate_AnUntrustedServerIsStillTheServers(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{ClientCertificate: true})
	other := gnmitest.New(t, gnmitest.Options{})
	c, _, err := gnmi.ClientFor(gnmitest.NewDeviceWith(t, s, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: other.AuthorityPEM(), devicetls.ClientCertificateProperty: true,
	}), "switch1", s.ClientSecrets())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.Capabilities(ctxOf(t))
	var untrusted *devicetls.UntrustedError
	var refused *devicetls.ClientCertificateRefusedError
	if !errors.As(err, &untrusted) || errors.As(err, &refused) {
		t.Fatalf("Capabilities = %v, want an UntrustedError and no client-certificate refusal", err)
	}
}

// Package gnmitest: a gNMI device standing for a Server, through the real
// TLS and credential path a generic_gnmi device takes: the server's authority
// pinned as a record's tls_ca_pem pins one, so its certificate's name is
// checked too, or nothing for a plaintext server.
package gnmitest

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Device is a GNMICapable device: its target, credential mode and TLS.
type Device struct {
	*inventorytest.Stub
	// Target is what GNMITarget returns.
	Target string
	// Auth is what GNMIAuth returns.
	Auth string
	// Plaintext is what GNMIPlaintext returns.
	Plaintext bool
	// AllowPlaintext is what GNMIAllowPlaintextCredentials returns.
	AllowPlaintext bool
	// PrefixTarget is what GNMIPrefixTarget returns, as generic_gnmi's
	// gnmi_target property sets it.
	PrefixTarget string
	tls          devicetls.Settings
}

// NewDevice returns a device for s: password authentication when s asks for
// a login, a bearer token when it asks for one, none otherwise; and s's
// authority pinned (with mutual TLS when s requires it), or plaintext.
func NewDevice(t testing.TB, s *Server) *Device {
	t.Helper()
	props := map[string]inventory.PropertyValue{}
	if !s.opts.Plaintext {
		props[devicetls.CAPEMProperty] = s.AuthorityPEM()
		if s.opts.ClientCertificate {
			props[devicetls.ClientCertificateProperty] = true
		}
	}
	return NewDeviceWith(t, s, props)
}

// NewDeviceWith is NewDevice with the TLS properties props instead.
func NewDeviceWith(t testing.TB, s *Server, props map[string]inventory.PropertyValue) *Device {
	t.Helper()
	settings, err := devicetls.Parse(inventory.NewProperties(props))
	if err != nil {
		t.Fatal(err)
	}
	auth := gnmi.AuthNone
	switch {
	case s.opts.Token != "":
		auth = gnmi.AuthBearer
	case s.opts.Username != "" || s.opts.Password != "":
		auth = gnmi.AuthPassword
	}
	return &Device{
		Stub:   &inventorytest.Stub{StubName: "gnmi", Caps: []capability.Name{capability.NameGNMI, capability.NameNetworkAddressable}},
		Target: s.Addr(), Auth: auth, Plaintext: s.opts.Plaintext, tls: settings,
	}
}

// GNMITarget implements capability.GNMICapable.
func (d *Device) GNMITarget() string { return d.Target }

// GNMIAuth implements capability.GNMICapable.
func (d *Device) GNMIAuth() string { return d.Auth }

// GNMIPlaintext implements capability.GNMICapable.
func (d *Device) GNMIPlaintext() bool { return d.Plaintext }

// GNMIAllowPlaintextCredentials implements capability.GNMICapable.
func (d *Device) GNMIAllowPlaintextCredentials() bool { return d.AllowPlaintext }

// GNMIPrefixTarget implements gnmi.PrefixTargeted.
func (d *Device) GNMIPrefixTarget() string { return d.PrefixTarget }

// TLSSettings implements devicetls.Configured.
func (d *Device) TLSSettings() devicetls.Settings { return d.tls }

// IPAddress implements capability.NetworkAddressableCapable.
func (d *Device) IPAddress() string { return "127.0.0.1" }

// Secrets returns the credential s asks for, flattened as a task's secrets
// are, with s's client certificate when it requires one.
func Secrets(s *Server) map[string]string {
	out := map[string]string{}
	if s.opts.Username != "" || s.opts.Password != "" {
		out[wire.SecretUsername], out[wire.SecretPassword] = s.opts.Username, s.opts.Password
	}
	if s.opts.Token != "" {
		out[wire.SecretBearerToken] = s.opts.Token
	}
	for k, v := range s.client {
		out[k] = v
	}
	return out
}

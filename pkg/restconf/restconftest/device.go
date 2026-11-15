// Package restconftest: a RESTCONF device standing for a Server, through the
// real TLS pin and credential path a generic_restconf device takes.
package restconftest

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Device is a RestconfCapable device: its origin, credential mode and TLS,
// pinned to a Server's certificate as a record's tls_fingerprint pins one.
type Device struct {
	*inventorytest.Stub
	// Base is the origin RestconfBaseURL returns.
	Base string
	// Auth is the credential mode RestconfAuth returns.
	Auth string
	// AllowPlaintext is what RestconfAllowPlaintextCredentials returns.
	AllowPlaintext bool
	tls            devicetls.Settings
}

// NewDevice returns a device for s, sending basic authentication.
func NewDevice(t testing.TB, s *Server) *Device {
	t.Helper()
	settings, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{
		devicetls.FingerprintProperty: devicetls.Fingerprint(s.Certificate()),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return &Device{
		Stub: &inventorytest.Stub{StubName: "restconf", Caps: []capability.Name{capability.NameRestconf, capability.NameNetworkAddressable}},
		Base: s.URL, Auth: "basic", tls: settings,
	}
}

// RestconfBaseURL implements capability.RestconfCapable.
func (d *Device) RestconfBaseURL() string { return d.Base }

// RestconfAuth implements capability.RestconfCapable.
func (d *Device) RestconfAuth() string { return d.Auth }

// RestconfAllowPlaintextCredentials implements capability.RestconfCapable.
func (d *Device) RestconfAllowPlaintextCredentials() bool { return d.AllowPlaintext }

// TLSSettings implements devicetls.Configured.
func (d *Device) TLSSettings() devicetls.Settings { return d.tls }

// IPAddress implements capability.NetworkAddressableCapable.
func (d *Device) IPAddress() string { return "127.0.0.1" }

// Secrets returns the credential s asks for, flattened as a task's secrets
// are.
func Secrets(s *Server) map[string]string {
	return map[string]string{wire.SecretUsername: s.opts.Username, wire.SecretPassword: s.opts.Password}
}

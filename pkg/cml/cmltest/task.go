// Package cmltest: a controller device and a task context for testing the
// virt.cml methods against Controller, through the real TLS pin and login
// path a cml_controller device takes.
package cmltest

import (
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Device is a cml_controller standing for c: its base URL, and its TLS
// pinned to c's own certificate, as a record's tls_fingerprint pins one.
type Device struct {
	*inventorytest.Stub
	base string
	tls  devicetls.Settings
}

// NewDevice returns the device for c.
func NewDevice(t *testing.T, c *Controller) *Device {
	t.Helper()
	settings, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{
		devicetls.FingerprintProperty: devicetls.Fingerprint(c.Certificate()),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return &Device{
		Stub: &inventorytest.Stub{StubName: "cml", Caps: []capability.Name{capability.NameCMLAPI, capability.NameNetworkAddressable}},
		base: c.URL, tls: settings,
	}
}

// CMLBaseURL implements capability.CMLAPICapable.
func (d *Device) CMLBaseURL() string { return d.base }

// CMLPlaintextWarning implements capability.CMLAPICapable.
func (d *Device) CMLPlaintextWarning() string { return "" }

// CMLConsoleAddress implements capability.CMLAPICapable.
func (d *Device) CMLConsoleAddress() (string, int) { return "127.0.0.1", 22 }

// TLSSettings implements devicetls.Configured.
func (d *Device) TLSSettings() devicetls.Settings { return d.tls }

// Context is a task's context: the secrets it is handed (the controller's
// login, and a seed when a test adds one) and what the task records.
type Context struct {
	mu sync.Mutex
	// Secrets are what InjectSecrets hands the method.
	Secrets map[string]string
	// Stats are what the task set, by key.
	Stats map[string]any
}

// NewContext returns a context holding c's login.
func NewContext(c *Controller) *Context {
	return &Context{Secrets: map[string]string{wire.SecretUsername: c.Username, wire.SecretPassword: c.Password}, Stats: map[string]any{}}
}

// InjectSecrets implements sdk.RunbookContext.
func (x *Context) InjectSecrets() map[string]string {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make(map[string]string, len(x.Secrets))
	for k, v := range x.Secrets {
		out[k] = v
	}
	return out
}

// SetStat implements sdk.RunbookContext.
func (x *Context) SetStat(key string, value any) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.Stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (x *Context) EmitFact(string, any) error { return nil }

// Inverse returns the undo the task recorded, and false when it recorded
// none.
func (x *Context) Inverse() (map[string]any, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	inv, ok := x.Stats[sdk.StatInverse].(map[string]any)
	return inv, ok
}

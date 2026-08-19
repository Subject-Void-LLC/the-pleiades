package feature_test

import (
	"context"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/win/feature"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// See internal/catalog/svc/windows's own windows_test.go doc comment:
// this file is the identical split between plumbing/registration tests
// here (no network needed to fail correctly) and the gated Release Gate
// for a live device.

type winrmDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *winrmDevice) WinRMHost() string   { return d.host }
func (d *winrmDevice) WinRMPort() int      { return d.port }
func (d *winrmDevice) DISMLogPath() string { return `C:\Windows\Logs\DISM\dism.log` }

func device() *winrmDevice {
	return &winrmDevice{
		Stub: &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWindowsFeature}},
		host: "127.0.0.1",
		port: 1,
	}
}

type ctxStub struct{ stats map[string]any }

func (c *ctxStub) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "secret"}
}
func (c *ctxStub) SetStat(k string, v any) error  { c.stats[k] = v; return nil }
func (c *ctxStub) EmitFact(k string, v any) error { return c.SetStat(k, v) }

func invoke(t *testing.T, fqcn string, params map[string]any) error {
	t.Helper()
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	_, err := desc.Invoke(context.Background(), &ctxStub{stats: map[string]any{}}, device(), params)
	return err
}

func TestRegistered(t *testing.T) {
	for _, fqcn := range []string{"win.feature.install", "win.feature.remove"} {
		t.Run(fqcn, func(t *testing.T) {
			desc, ok := collection.Lookup(fqcn)
			if !ok {
				t.Fatalf("%s is not registered", fqcn)
			}
			if desc.Manifest.Status != collection.StatusImplemented || desc.Invoke == nil {
				t.Fatalf("Status = %v, Invoke nil = %v; want implemented with an implementation",
					desc.Manifest.Status, desc.Invoke == nil)
			}
			caps := desc.Manifest.RequiredCapabilities
			if len(caps) != 1 || caps[0] != capability.NameWindowsFeature {
				t.Errorf("RequiredCapabilities = %v, want [WindowsFeatureCapable]", caps)
			}
			transports := desc.Manifest.SupportedTransports
			if len(transports) != 1 || transports[0] != "winrm" {
				t.Errorf("SupportedTransports = %v, want [winrm]", transports)
			}
			if !desc.Manifest.Reversibility.Reversible {
				t.Error("Reversible = false, want true")
			}
			if desc.Manifest.Reversibility.Notes == "" {
				t.Error("declares reversible with no reason")
			}
			if desc.Manifest.Doc.Summary == "" {
				t.Error("Doc.Summary is empty")
			}
		})
	}
}

func TestRefusesMissingName(t *testing.T) {
	for _, fqcn := range []string{"win.feature.install", "win.feature.remove"} {
		t.Run(fqcn, func(t *testing.T) {
			err := invoke(t, fqcn, nil)
			if err == nil {
				t.Fatal("expected a refusal for a missing name")
			}
			if !strings.Contains(err.Error(), fqcn) {
				t.Errorf("error = %v, want it to name the method", err)
			}
			if !strings.Contains(err.Error(), "name") {
				t.Errorf("error = %v, want it to mention the missing name parameter", err)
			}
		})
	}
}

func TestRefusesADeviceWithNoWinRMAccessors(t *testing.T) {
	desc, ok := collection.Lookup("win.feature.install")
	if !ok {
		t.Fatal("win.feature.install is not registered")
	}
	stub := &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWindowsFeature}}
	_, err := desc.Invoke(context.Background(), &ctxStub{stats: map[string]any{}}, stub, map[string]any{"name": "IIS-WebServerRole"})
	if err == nil || !strings.Contains(err.Error(), "does not implement its accessors") {
		t.Errorf("error = %v, want it to name the missing accessors", err)
	}
}

func TestUnreachableTargetReachesTheNetworkWithTheRightPlumbing(t *testing.T) {
	for _, fqcn := range []string{"win.feature.install", "win.feature.remove"} {
		t.Run(fqcn, func(t *testing.T) {
			err := invoke(t, fqcn, map[string]any{"name": "IIS-WebServerRole"})
			if err == nil {
				t.Fatal("expected an error dialing an unreachable target")
			}
			if !strings.Contains(err.Error(), "IIS-WebServerRole") {
				t.Errorf("error = %v, want it to name the feature, proving it got past parameter validation", err)
			}
		})
	}
}

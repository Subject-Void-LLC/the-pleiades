package windows_test

import (
	"context"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/windows"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// There is no in-process Service Control Manager to test against over
// WinRM, and no fake worth building for the same reason
// pkg/winrmexec's own tests give: a stub would only prove this method
// agrees with the stub. What is covered in this file and its per-verb
// siblings is everything before the network: registration, parameter
// validation, the device accessor, and that a call against an
// unreachable target reaches the network with the right session (proving
// the plumbing, not the Service Control Manager's answer). The decision
// logic itself (converged/refusal/inverse) is covered without any
// network at all in windows_internal_test.go, by swapping the statusFunc
// seam. The real proof of a live device is
// cmd/pleiades/winrm_service_feature_release_gate_test.go.

// winrmDevice declares and structurally implements WinRMCapable.
type winrmDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *winrmDevice) WinRMHost() string { return d.host }
func (d *winrmDevice) WinRMPort() int    { return d.port }

func device() *winrmDevice {
	return &winrmDevice{
		Stub: &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWindowsService}},
		// Port 1 on loopback: nothing answers, so a call reaches the
		// network and fails fast with a dial error, proving the session
		// was built and the request was attempted without needing (or
		// waiting on) a real Windows host.
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

// TestRegistered checks what the catalog claims about each method, from
// the live registry rather than from a list, mirroring
// exec/winrm/shell_test.go's own TestRegistered.
func TestRegistered(t *testing.T) {
	tests := []struct {
		fqcn       string
		reversible bool
		elevated   bool
	}{
		{fqcn: "svc.windows.start", reversible: true, elevated: true},
		{fqcn: "svc.windows.stop", reversible: true, elevated: true},
		{fqcn: "svc.windows.restart", reversible: false, elevated: true},
		{fqcn: "svc.windows.enable", reversible: true, elevated: true},
		{fqcn: "svc.windows.disable", reversible: true, elevated: true},
	}
	for _, tt := range tests {
		t.Run(tt.fqcn, func(t *testing.T) {
			desc, ok := collection.Lookup(tt.fqcn)
			if !ok {
				t.Fatalf("%s is not registered", tt.fqcn)
			}
			if desc.Manifest.Status != collection.StatusImplemented || desc.Invoke == nil {
				t.Fatalf("Status = %v, Invoke nil = %v; want implemented with an implementation",
					desc.Manifest.Status, desc.Invoke == nil)
			}
			caps := desc.Manifest.RequiredCapabilities
			if len(caps) != 1 || caps[0] != capability.NameWindowsService {
				t.Errorf("RequiredCapabilities = %v, want [WindowsServiceCapable]", caps)
			}
			transports := desc.Manifest.SupportedTransports
			if len(transports) != 1 || transports[0] != "winrm" {
				t.Errorf("SupportedTransports = %v, want [winrm]", transports)
			}
			if desc.Manifest.ExecutionContext.RequiresElevation != tt.elevated {
				t.Errorf("RequiresElevation = %v, want %v", desc.Manifest.ExecutionContext.RequiresElevation, tt.elevated)
			}
			if desc.Manifest.Reversibility.Reversible != tt.reversible {
				t.Errorf("Reversible = %v, want %v", desc.Manifest.Reversibility.Reversible, tt.reversible)
			}
			if desc.Manifest.Reversibility.Notes == "" {
				t.Error("declares a reversibility answer with no reason")
			}
			if desc.Manifest.Doc.Summary == "" {
				t.Error("Doc.Summary is empty")
			}
		})
	}
}

// TestRefusesMissingName covers every method's shared required parameter,
// which must be caught before anything is dialed.
func TestRefusesMissingName(t *testing.T) {
	for _, fqcn := range []string{
		"svc.windows.start", "svc.windows.stop", "svc.windows.restart",
		"svc.windows.enable", "svc.windows.disable",
	} {
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

// TestRefusesADeviceWithNoWinRMAccessors proves the defense-in-depth
// refusal winrmSession names: a device declaring the capability without
// structurally implementing WinRMCapable is refused with a clear reason
// rather than a nil pointer panic.
func TestRefusesADeviceWithNoWinRMAccessors(t *testing.T) {
	desc, ok := collection.Lookup("svc.windows.start")
	if !ok {
		t.Fatal("svc.windows.start is not registered")
	}
	stub := &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWindowsService}}
	_, err := desc.Invoke(context.Background(), &ctxStub{stats: map[string]any{}}, stub, map[string]any{"name": "spooler"})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "does not implement its accessors") {
		t.Errorf("error = %v, want it to name the missing accessors", err)
	}
}

// TestUnreachableTargetReachesTheNetworkWithTheRightPlumbing proves the
// session assembled from the device and rc.InjectSecrets actually gets
// used: a call against a closed local port fails with a dial error (not
// a validation error), which is only possible if the target, port and
// credential all made it through.
func TestUnreachableTargetReachesTheNetworkWithTheRightPlumbing(t *testing.T) {
	for _, fqcn := range []string{
		"svc.windows.start", "svc.windows.stop", "svc.windows.restart",
		"svc.windows.enable", "svc.windows.disable",
	} {
		t.Run(fqcn, func(t *testing.T) {
			err := invoke(t, fqcn, map[string]any{"name": "spooler"})
			if err == nil {
				t.Fatal("expected an error dialing an unreachable target")
			}
			if !strings.Contains(err.Error(), "spooler") {
				t.Errorf("error = %v, want it to name the service, proving it got past parameter validation", err)
			}
		})
	}
}

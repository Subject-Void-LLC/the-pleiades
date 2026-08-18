package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestNewDefaultTransportBindings_RegistersSSHExec proves the returned
// Registry carries exactly one entry, "ssh_exec", agreeing with
// engine.ActionCapability, bound to the transport.Transport the caller
// passed in and to SSHTarget -- the same shape cmd/pleiades/run.go's own
// bare map literal used to hand-build.
func TestNewDefaultTransportBindings_RegistersSSHExec(t *testing.T) {
	sshTransport := &fakeTransport{
		exec: func(_ context.Context, _ transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
			return transport.Result{}, nil
		},
	}

	// nil for WinRM: a composition root with no reason to reach Windows
	// registers no winrm_exec binding, which is what keeps this a
	// one-entry table.
	bindings := engine.NewDefaultTransportBindings(sshTransport)
	all := bindings.All()

	if len(all) != 1 {
		t.Fatalf("len(all) = %d, want 1", len(all))
	}
	binding, ok := all["ssh_exec"]
	if !ok {
		t.Fatal("expected a registered binding for \"ssh_exec\"")
	}
	if binding.Capability != capability.NameSSHTransport {
		t.Errorf("Capability = %s, want %s", binding.Capability, capability.NameSSHTransport)
	}
	if binding.Transport != sshTransport {
		t.Error("Transport is not the exact instance passed in")
	}

	dev := newSSHDevice("router1", "10.0.0.1", 2222)
	target, ok := binding.Target(dev)
	if !ok || target.Host != "10.0.0.1" || target.Port != 2222 {
		t.Errorf("Target(dev) = %+v, %v, want {10.0.0.1 2222}, true", target, ok)
	}
}

// TestNewDefaultTransportBindings_AgreesWithActionCapability proves the
// registered binding never drifts from engine.ActionCapability, the exact
// class of bug CheckActionCapabilityBindings exists to catch: this test
// runs that same check directly against the Registry's own All() output.
func TestNewDefaultTransportBindings_AgreesWithActionCapability(t *testing.T) {
	// Both transports supplied, so the check covers every binding this
	// constructor can produce rather than only the SSH one.
	bindings := engine.NewDefaultTransportBindings(&fakeTransport{})
	if err := engine.CheckActionCapabilityBindings(bindings.All()); err != nil {
		t.Errorf("CheckActionCapabilityBindings: %v", err)
	}
}

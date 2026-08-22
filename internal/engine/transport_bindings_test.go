package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestNewDefaultTransportBindings_RegistersAllThree proves the returned
// Registry carries exactly four entries ("ssh_exec", "serial_exec",
// "serialtcp_exec", "telnet_exec"), each agreeing with
// engine.ActionCapability, bound to the transport.Transport the caller
// passed in and to its own Target function -- the same shape
// cmd/pleiades/run.go's own bare map literal used to hand-build before
// this constructor existed.
func TestNewDefaultTransportBindings_RegistersAllThree(t *testing.T) {
	sshTransport := &fakeTransport{
		exec: func(_ context.Context, _ transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
			return transport.Result{}, nil
		},
	}
	serialTransport := &fakeTransport{}
	serialtcpTransport := &fakeTransport{}
	telnetTransport := &fakeTransport{}

	bindings := engine.NewDefaultTransportBindings(sshTransport, serialTransport, serialtcpTransport, telnetTransport)
	all := bindings.All()

	if len(all) != 4 {
		t.Fatalf("len(all) = %d, want 4", len(all))
	}

	t.Run("ssh_exec", func(t *testing.T) {
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
		if binding.RequireOptInParam != "" {
			t.Errorf("RequireOptInParam = %q, want empty: SSH needs no opt-in gate", binding.RequireOptInParam)
		}

		dev := newSSHDevice("router1", "10.0.0.1", 2222)
		target, ok := binding.Target(dev)
		ep, epOK := target.Endpoint.(transport.NetworkEndpoint)
		if !ok || !epOK || ep.Host != "10.0.0.1" || ep.Port != 2222 {
			t.Errorf("Target(dev) = %+v, %v, want {10.0.0.1 2222}, true", target, ok)
		}
	})

	t.Run("serial_exec", func(t *testing.T) {
		binding, ok := all["serial_exec"]
		if !ok {
			t.Fatal("expected a registered binding for \"serial_exec\"")
		}
		if binding.Capability != capability.NameSerial {
			t.Errorf("Capability = %s, want %s", binding.Capability, capability.NameSerial)
		}
		if binding.Transport != serialTransport {
			t.Error("Transport is not the exact instance passed in")
		}
		if binding.RequireOptInParam != "" {
			t.Errorf("RequireOptInParam = %q, want empty: local serial needs no opt-in gate", binding.RequireOptInParam)
		}
	})

	t.Run("serialtcp_exec", func(t *testing.T) {
		binding, ok := all["serialtcp_exec"]
		if !ok {
			t.Fatal("expected a registered binding for \"serialtcp_exec\"")
		}
		if binding.Capability != capability.NameRawPassthrough {
			t.Errorf("Capability = %s, want %s", binding.Capability, capability.NameRawPassthrough)
		}
		if binding.Transport != serialtcpTransport {
			t.Error("Transport is not the exact instance passed in")
		}
		if binding.RequireOptInParam != engine.ParamInsecureRawPassthrough {
			t.Errorf("RequireOptInParam = %q, want %q: raw TCP passthrough has zero authentication and zero encryption", binding.RequireOptInParam, engine.ParamInsecureRawPassthrough)
		}

		dev := newRawPassthroughDevice("console-server1", "10.0.0.5", 7001)
		target, ok := binding.Target(dev)
		ep, epOK := target.Endpoint.(transport.NetworkEndpoint)
		if !ok || !epOK || ep.Host != "10.0.0.5" || ep.Port != 7001 {
			t.Errorf("Target(dev) = %+v, %v, want {10.0.0.5 7001}, true", target, ok)
		}
	})

	t.Run("telnet_exec", func(t *testing.T) {
		binding, ok := all["telnet_exec"]
		if !ok {
			t.Fatal("expected a registered binding for \"telnet_exec\"")
		}
		if binding.Capability != capability.NameTelnet {
			t.Errorf("Capability = %s, want %s", binding.Capability, capability.NameTelnet)
		}
		if binding.Transport != telnetTransport {
			t.Error("Transport is not the exact instance passed in")
		}
		if binding.RequireOptInParam != engine.ParamInsecureTelnet {
			t.Errorf("RequireOptInParam = %q, want %q: bare Telnet has zero encryption at the protocol level", binding.RequireOptInParam, engine.ParamInsecureTelnet)
		}

		dev := newTelnetDevice("console-server2", "10.0.0.6", 23)
		target, ok := binding.Target(dev)
		ep, epOK := target.Endpoint.(transport.NetworkEndpoint)
		if !ok || !epOK || ep.Host != "10.0.0.6" || ep.Port != 23 {
			t.Errorf("Target(dev) = %+v, %v, want {10.0.0.6 23}, true", target, ok)
		}
	})
}

// TestNewDefaultTransportBindings_AgreesWithActionCapability proves
// every registered binding never drifts from engine.ActionCapability,
// the exact class of bug CheckActionCapabilityBindings exists to catch:
// this test runs that same check directly against the Registry's own
// All() output, with all four transports supplied so the check covers
// every binding this constructor can produce.
func TestNewDefaultTransportBindings_AgreesWithActionCapability(t *testing.T) {
	bindings := engine.NewDefaultTransportBindings(&fakeTransport{}, &fakeTransport{}, &fakeTransport{}, &fakeTransport{})
	if err := engine.CheckActionCapabilityBindings(bindings.All()); err != nil {
		t.Errorf("CheckActionCapabilityBindings: %v", err)
	}
}

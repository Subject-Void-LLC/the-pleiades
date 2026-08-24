package engine

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// NewDefaultTransportBindings builds the one shared, capability-keyed
// registry of TransportBinding every composition root that needs a real
// transport should build from, rather than each hand-rolling its own
// map[string]TransportBinding literal. Before this existed,
// cmd/pleiades/run.go's own bindings map was a bare, unregistered literal
// with exactly one entry ("ssh_exec") and no second caller -- the "Registry
// for transport selection" Phase 16 (Native Go Execution Adapter)'s own
// Pattern Entry Gate calls for did not actually exist anywhere in the
// codebase. This is pkg/registry.Registry[TransportBinding]'s first use
// for this purpose: a fourth consumer of the one shared generic Registry
// primitive (alongside pkg/capability's vocabulary, pkg/collection's
// method registry, and the inventory device-type table), not a new
// hand-rolled map.
//
// sshTransport, serialTransport, serialtcpTransport, and telnetTransport
// are the real transport.Transport values a caller has already
// constructed (internal/transport/ssh.New(...),
// internal/transport/serial.New(...), internal/transport/serialtcp.New(...),
// internal/transport/telnet.New(...)); this function does not construct
// any of them itself, since a composition root's own choice of options
// (known_hosts path, retry/breaker tuning, serial read timeouts) is not
// this package's decision to make.
//
// Adding a second real COMMAND-ORIENTED transport (WinRM, for one) is a
// new MustRegister call here plus a new transport.Transport
// implementation elsewhere, never a change to transportActionExecutor:
// the same "data,
// not a type switch" property TransportBinding's own doc comment already
// claims for the map form holds identically for the Registry form.
// serial_exec, serialtcp_exec, and telnet_exec (Phase 73) are the
// second, third, and fourth real proof of that claim: none needed a
// change to transportActionExecutor.Execute beyond the
// RequireOptInParam gate, which is itself data on the binding, not a
// type switch on the fqcn.
//
// NETCONF was named here as a future entry and is not one: it is not
// Exec-shaped (no command string, no stdout, no exit code), so it has no
// TransportBinding and never will. See pkg/datastore, the port the
// structured-configuration protocols use instead.
func NewDefaultTransportBindings(sshTransport, serialTransport, serialtcpTransport, telnetTransport transport.Transport) *registry.Registry[TransportBinding] {
	bindings := registry.New[TransportBinding]()
	bindings.MustRegister("ssh_exec", TransportBinding{
		Capability: ActionCapability["ssh_exec"],
		Transport:  sshTransport,
		Target:     SSHTarget,
	})
	bindings.MustRegister("serial_exec", TransportBinding{
		Capability: ActionCapability["serial_exec"],
		Transport:  serialTransport,
		Target:     SerialTarget,
	})
	bindings.MustRegister("serialtcp_exec", TransportBinding{
		Capability:        ActionCapability["serialtcp_exec"],
		Transport:         serialtcpTransport,
		Target:            RawPassthroughTarget,
		RequireOptInParam: ParamInsecureRawPassthrough,
	})
	bindings.MustRegister("telnet_exec", TransportBinding{
		Capability:        ActionCapability["telnet_exec"],
		Transport:         telnetTransport,
		Target:            TelnetTarget,
		RequireOptInParam: ParamInsecureTelnet,
	})
	return bindings
}

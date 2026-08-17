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
// sshTransport is the one real transport.Transport a caller has already
// constructed (internal/transport/ssh.New(...)); this function does not
// construct it itself, since a composition root's own choice of
// sshtransport.Options (known_hosts path, retry/breaker tuning) is not
// this package's decision to make.
//
// Adding a second real transport (WinRM, NETCONF, ...) is a new
// MustRegister call here plus a new transport.Transport implementation
// elsewhere, never a change to transportActionExecutor: the same "data,
// not a type switch" property TransportBinding's own doc comment already
// claims for the map form holds identically for the Registry form.
// winrmTransport is the second one, and it arrives the same way for the
// same reason: this package must not import a concrete driver, so the
// composition root constructs internal/transport/winrm and hands the
// result in. A nil winrmTransport registers no winrm_exec binding at
// all, which is how a composition root that has no reason to reach
// Windows (a test, or a build that never sees a Windows device) avoids
// carrying one.
func NewDefaultTransportBindings(sshTransport, winrmTransport transport.Transport) *registry.Registry[TransportBinding] {
	bindings := registry.New[TransportBinding]()
	bindings.MustRegister("ssh_exec", TransportBinding{
		Capability: ActionCapability["ssh_exec"],
		Transport:  sshTransport,
		Target:     SSHTarget,
	})
	if winrmTransport != nil {
		bindings.MustRegister("winrm_exec", TransportBinding{
			Capability: ActionCapability["winrm_exec"],
			Transport:  winrmTransport,
			Target:     WinRMTarget,
		})
	}
	return bindings
}

// Package capability defines the blessed capability vocabulary: the typed
// Name each device advertises, the behavioral interfaces those names bind
// to, and the registry that guarantees a device cannot advertise a
// capability it does not structurally implement.
//
// Before this rewrite, this package and internal/inventory both defined a
// CiscoIOSCapable interface with different method sets, and neither was
// implemented by any concrete type. That is the exact collision
// namespacing exists to prevent (PLAN.md Section 1b, Section 8). There is
// now exactly one definition of each capability, here.
//
// The vocabulary registry below is built on pkg/registry.Registry
// (Phase 6's Section 25 "typed generic Registry"), so exactly one Registry
// implementation exists in this codebase rather than this package's own
// hand-rolled map sitting next to the shared one.
package capability

import "github.com/SubjectVoidLLC/the-pleiades/pkg/registry"

// Name is a capability identifier. It is always typed at the call site;
// PLAN.md Section 1b requires this, since collections resolve against
// capabilities, not concrete device types.
type Name string

// The blessed capability vocabulary. Each has a Descriptor registered in
// init, below, binding it to the interface a device must actually
// implement to advertise it.
const (
	NameSSHTransport Name = "SSHTransportCapable"
	NameCiscoIOS     Name = "CiscoIOSCapable"
	NameLinux        Name = "LinuxCapable"
)

// SSHTransportCapable is satisfied by any device reachable over SSH.
type SSHTransportCapable interface {
	SSHHost() string
	SSHPort() int
}

// CiscoIOSCapable is satisfied by devices running Cisco IOS or IOS-XE.
type CiscoIOSCapable interface {
	// IOSVersion returns the detected or configured firmware version.
	IOSVersion() string

	// SupportsNETCONF reports whether NETCONF/YANG is enabled.
	SupportsNETCONF() bool
}

// LinuxCapable is satisfied by standard Linux servers.
type LinuxCapable interface {
	KernelVersion() string
	Distribution() string
}

// Descriptor is one entry in the capability registry: the name, its
// optional parent in the Section 8 hierarchy (PackageManagerCapable ->
// AptCapable), and the structural check that proves a value actually
// implements the bound interface.
type Descriptor struct {
	Name   Name
	Parent Name

	// Assert reports whether item structurally satisfies this
	// capability's interface. It takes `any` deliberately: this is the
	// one seam where the registry must accept every concrete device
	// type, and Go has no narrower way to express that.
	Assert func(item any) bool
}

// vocabulary holds every blessed capability. It is populated by init below,
// not a runtime Register call from elsewhere, because the core vocabulary
// is closed: a third party proposing a new capability registers it in
// their own package (Section 8 governance), it does not inject into this
// one.
var vocabulary = registry.New[Descriptor]()

// Register adds a Descriptor to the blessed vocabulary. It panics on a
// duplicate name: two different structural definitions of the same
// capability name is the exact collision this package exists to prevent,
// so it must fail loudly at init time rather than silently pick one.
func Register(d Descriptor) {
	vocabulary.MustRegister(string(d.Name), d)
}

// Lookup returns the Descriptor registered for name.
func Lookup(name Name) (Descriptor, bool) {
	return vocabulary.Get(string(name))
}

// Implements reports whether item structurally satisfies the interface
// bound to name. Concrete HasCapability implementations call this so that
// returning true is a compiler-checked guarantee, not an assertion left to
// a comment.
func Implements(item any, name Name) bool {
	d, ok := vocabulary.Get(string(name))
	if !ok {
		return false
	}
	return d.Assert(item)
}

func init() {
	Register(Descriptor{
		Name:   NameSSHTransport,
		Assert: func(item any) bool { _, ok := item.(SSHTransportCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameCiscoIOS,
		Assert: func(item any) bool { _, ok := item.(CiscoIOSCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameLinux,
		Assert: func(item any) bool { _, ok := item.(LinuxCapable); return ok },
	})
}

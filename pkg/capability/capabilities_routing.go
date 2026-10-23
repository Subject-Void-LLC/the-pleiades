// Package capability: the routing suite and the software switch onboarding
// finds answering on a generic_ssh device (FRRouting, Open vSwitch).
package capability

const (
	NameFRR         Name = "FRRCapable"
	NameOpenVSwitch Name = "OpenVSwitchCapable"
)

// FRRCapable is satisfied by a device running the FRRouting suite, whose
// vtysh the connecting account may use. It has no parent: FRR is reached by
// running vtysh over a shell, not through a CLI prompt, so it is neither
// NetworkCLICapable nor any vendor's CLI. Onboarding grants it only when
// vtysh answers "show version" as the connecting account, so a grant
// proves permission as well as presence.
type FRRCapable interface {
	// FRRVersion returns the FRR version vtysh reported, e.g. "10.1.1".
	FRRVersion() string
}

// OpenVSwitchCapable is satisfied by a device running Open vSwitch whose
// database the connecting account may read and change through ovs-vsctl.
// Onboarding grants it only when "ovs-vsctl show" succeeds as that account.
type OpenVSwitchCapable interface {
	// OVSVersion returns the Open vSwitch version ovs-vsctl reported, e.g.
	// "2.17.9".
	OVSVersion() string
}

func init() {
	Register(Descriptor{
		Name:   NameFRR,
		Assert: func(item any) bool { _, ok := item.(FRRCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameOpenVSwitch,
		Assert: func(item any) bool { _, ok := item.(OpenVSwitchCapable); return ok },
	})
}

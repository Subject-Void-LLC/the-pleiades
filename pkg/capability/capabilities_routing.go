// Package capability: the routing suite and the software switch onboarding
// finds answering on a generic_ssh device (FRRouting, Open vSwitch), and the
// network model a change is checked against first (Batfish).
package capability

const (
	NameFRR         Name = "FRRCapable"
	NameOpenVSwitch Name = "OpenVSwitchCapable"
	NameBatfishAPI  Name = "BatfishAPICapable"
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

// BatfishAPICapable is satisfied by a Batfish service: the network model
// net.batfish.check uploads configurations to and asks about, over its REST
// API. Batfish has no authentication of its own, so the batfish device type
// reaches it over plain HTTP only at a loopback address unless its record
// says otherwise, and over HTTPS with the device's own TLS settings.
type BatfishAPICapable interface {
	// BatfishBaseURL returns the service's base URL, e.g.
	// "http://127.0.0.1:9996".
	BatfishBaseURL() string
	// BatfishPlaintextWarning returns, when the record allows plain HTTP to
	// an address that is not loopback, the warning every run shows, and ""
	// otherwise.
	BatfishPlaintextWarning() string
}

func init() {
	Register(Descriptor{
		Name:   NameBatfishAPI,
		Assert: func(item any) bool { _, ok := item.(BatfishAPICapable); return ok },
	})
	Register(Descriptor{
		Name:   NameFRR,
		Assert: func(item any) bool { _, ok := item.(FRRCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameOpenVSwitch,
		Assert: func(item any) bool { _, ok := item.(OpenVSwitchCapable); return ok },
	})
}

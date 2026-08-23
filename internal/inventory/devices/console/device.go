// Package console holds the concrete device type implementation for gear
// whose only management path is a console ("console_device"): a directly
// cabled serial line, a console or terminal server port (raw TCP
// passthrough or RFC 2217), or a bare Telnet session. Scaffolded by
// pleiades forge new-device (internal/inventory/devicescaffold), then
// hand-completed with real capability accessors, the same route
// internal/inventory/devices/container took.
//
// This package's init registers NewDevice into the shared record.Types
// registry under "console_device". internal/inventory/factory.go's
// NewItemFactory draws its batteries-included set from that registry
// rather than importing this package by name, so a blank import in
// internal/inventory/builtins.go is what makes this type reachable from
// the stock binary at all. This package itself never imports
// internal/inventory, only the leaf internal/inventory/record package
// plus pkg/inventory, pkg/capability, pkg/policy and pkg/serialline, so
// internal/inventory can depend on it (via that blank import) without a
// cycle back.
//
// # Why this device type exists
//
// Phase 73 shipped four capabilities (SerialCapable,
// RawPassthroughCapable, RFC2217Capable, TelnetCapable), three real
// transports behind them, three TransportBinding entries, three
// engine.ActionCapability rows and a documented user-facing feature, and
// **zero device types implementing any of the four**. Every
// serial_exec, serialtcp_exec and telnet_exec task was therefore refused
// twice over: validate.CapabilityRule rejected the runbook because no
// device had the capability, and engine.SerialTarget's type assertion
// would have failed anyway. That is the identical defect Phase 73's own
// Workstream A had just fixed for DockerCapable, shipped again in the
// same commit, because the guard that workstream added only walks the
// Collection catalog and a transport fqcn is not a Collection method.
// See FAILURE_PATTERNS.md; internal/archtest's
// TestDispatchableTransportCapabilitiesAreSatisfiable is the guard that
// now covers the other half.
//
// # Why the capabilities are declared conditionally
//
// Every other device type in this repository declares its whole baseline
// unconditionally: linux.Server is always SSHTransportCapable,
// container.Host is always DockerCapable. This one does not, and the
// difference is not a stylistic one.
//
// Those baselines are additional FACTS about one device, and they are all
// true at once: a container host really is reachable over SSH and really
// does have a Docker endpoint. These four are ALTERNATIVE ways to reach
// one device, and a real console target has exactly one of them: a
// switch cabled to a terminal server is not also on the local host's
// /dev/ttyUSB0. Declaring all four unconditionally would make
// validate.CapabilityRule answer "yes, serial_exec is fine" for a device
// that is only on Telnet, and the mistake would surface as a dial
// against an empty device name at execution instead of as a finding at
// plan time. AGENTS.md's Architecture Principle 5 ("type safety moves
// left ... never at task 47 of 200") is exactly the rule that decides
// this, and validate.CapabilityRule is the mechanism it names.
//
// So each capability is declared only when the record actually carries
// that path's configuration, which is the honest reading of "a
// capability is what a device CAN do": a device with no serial_device
// property cannot be reached over a local serial line, whatever type it
// is. Classification-derived capabilities (rec.Capabilities) are still
// unioned in on top, unchanged, so Phase 32's granularity decision holds
// here as it does everywhere else: classification can only add.
package console

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

func init() {
	record.RegisterType("console_device", NewDevice)
}

// Device implements inventory.InventoryItem plus, structurally, all four
// of capability.SerialCapable, capability.RawPassthroughCapable,
// capability.RFC2217Capable and capability.TelnetCapable.
//
// Structurally is the operative word: the accessors below exist on every
// Device, which is what makes capability.Implements true and what the
// engine.SerialTarget/RawPassthroughTarget/TelnetTarget type assertions
// need. Which of the four a given Device DECLARES is decided per record
// by NewDevice, for the reason this package's doc comment gives.
type Device struct {
	*record.Base

	// serialLine and rfc2217Line are parsed once, at construction, and
	// stored. They are not re-read from Properties on each call because
	// parsing can fail (a misspelled parity), and an accessor returning
	// only a Config has nowhere to report that failure: it would have to
	// swallow the error and hand back a default, which is precisely the
	// silent-corruption outcome serialline.ParseParity refuses to
	// produce. Failing in NewDevice instead means a bad line
	// configuration is a hydration error an operator sees at load time.
	serialLine  serialline.Config
	rfc2217Line serialline.Config
}

var _ inventory.InventoryItem = (*Device)(nil)

// NewDevice builds a Device from rec. It matches the record.Constructor
// shape (func(record.Record) (inventory.InventoryItem, error))
// record.RegisterType expects.
//
// It returns an error rather than a defaulted Device when a console
// property is present but unusable: a line setting that does not parse, a
// port outside the legal range, or a console-server host given with no
// port to reach it on. That is the rule .SPECIFICATION/IMPLEMENTATION.md's
// own hydration item states ("an unparseable value rejected at
// construction rather than defaulted"), and it matters more here than for
// an SSH port: a wrong baud rate or parity does not fail a serial line, it
// silently corrupts every byte crossing it.
func NewDevice(rec record.Record) (inventory.InventoryItem, error) {
	props := inventory.NewProperties(rec.Properties)

	paths, err := hydratePaths(props)
	if err != nil {
		return nil, err
	}

	caps := policy.UnionSlices(paths.declared, rec.Capabilities)
	return &Device{
		Base:        record.NewBase(rec, caps),
		serialLine:  paths.serialLine,
		rfc2217Line: paths.rfc2217Line,
	}, nil
}

// HasCapability checks the declared classification AND the structural
// registry assertion, so a true result is a guarantee, not a hope.
func (c *Device) HasCapability(name capability.Name) bool {
	return c.Declares(name) && capability.Implements(c, name)
}

// SerialDevice returns the operating system's name for the serial port
// this device is cabled to, read from the "serial_device" property.
//
// The value is passed through untouched, per capability.SerialCapable's
// own contract: it is opaque, and "COM3" is exactly as valid as
// "/dev/ttyUSB0". Nothing here parses, joins or validates it as a path.
func (c *Device) SerialDevice() serialline.Device {
	name, _ := c.Properties().String(propSerialDevice)
	return serialline.Device(name)
}

// SerialLine returns the line configuration NewDevice parsed for the
// local serial path: 9600 8-N-1 unless the record overrode a setting.
func (c *Device) SerialLine() serialline.Config {
	return c.serialLine
}

// RawPassthroughHost returns the console server's address for this
// device's raw TCP passthrough line, read from the
// "raw_passthrough_host" property.
func (c *Device) RawPassthroughHost() string {
	host, _ := c.Properties().String(propRawHost)
	return host
}

// RawPassthroughPort returns the console server's TCP port for this
// device's line, read from the "raw_passthrough_port" property.
//
// There is deliberately no default. A console server's per-line port is
// vendor-specific numbering (Digi starts at 2001, Opengear and Lantronix
// at 3001, Avocent elsewhere again), so unlike Telnet's port 23 there is
// no well-known value to fall back to, and guessing would dial a
// stranger's line on the same unit. NewDevice refuses a record that
// names a raw passthrough host without one.
func (c *Device) RawPassthroughPort() int {
	port, _ := c.Properties().Int(propRawPort)
	return port
}

// RFC2217Host returns the console server's address for this device's
// RFC 2217 line, read from the "rfc2217_host" property.
func (c *Device) RFC2217Host() string {
	host, _ := c.Properties().String(propRFC2217Host)
	return host
}

// RFC2217Port returns the console server's TCP port for this device's
// RFC 2217 line, read from the "rfc2217_port" property. It has no
// default for the same vendor-specific-numbering reason
// RawPassthroughPort does not.
func (c *Device) RFC2217Port() int {
	port, _ := c.Properties().Int(propRFC2217Port)
	return port
}

// RFC2217Line returns the line configuration NewDevice parsed for the
// RFC 2217 path.
//
// It is a separate set of properties from SerialLine's rather than a
// shared one, because the two capabilities are separate for the same
// reason: a device can be cabled locally, or sit behind a console
// server, and the settings that reach the far end of each are
// independent facts. Sharing one set would silently apply a local
// adapter's baud rate to a remote line.
func (c *Device) RFC2217Line() serialline.Config {
	return c.rfc2217Line
}

// TelnetHost returns this device's address for a bare Telnet session,
// read from the "telnet_host" property.
func (c *Device) TelnetHost() string {
	host, _ := c.Properties().String(propTelnetHost)
	return host
}

// TelnetPort returns this device's Telnet port, defaulting to 23.
//
// Unlike the two console-server ports above this one does have an honest
// default: 23 is Telnet's IANA-assigned well-known port, the same kind of
// convention linux.Server.SSHPort's fallback to 22 relies on.
func (c *Device) TelnetPort() int {
	if port, ok := c.Properties().Int(propTelnetPort); ok && port != 0 {
		return port
	}
	return defaultTelnetPort
}

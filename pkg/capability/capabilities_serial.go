package capability

import "github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"

const (
	NameSerial         Name = "SerialCapable"
	NameRawPassthrough Name = "RawPassthroughCapable"
	NameRFC2217        Name = "RFC2217Capable"
	NameTelnet         Name = "TelnetCapable"
)

// SerialCapable is satisfied by devices reachable over a local serial
// line: a directly attached USB-serial adapter, an onboard UART, or
// similar.
type SerialCapable interface {
	// SerialDevice identifies the serial port by whatever name the host
	// operating system assigns it ("/dev/ttyUSB0" on Linux,
	// "/dev/tty.usbserial-*" on macOS, "COM3" on Windows). It is OPAQUE
	// and must never be parsed, joined, or validated as a POSIX path:
	// "COM3" has no slashes and is not a filesystem path in the sense
	// path/filepath means, and even the POSIX names are a stable
	// identifier the operating system assigns, not a path this platform
	// should construct or manipulate.
	SerialDevice() serialline.Device

	// SerialLine returns the line's configuration (baud rate, data bits,
	// parity, stop bits).
	SerialLine() serialline.Config
}

// RawPassthroughCapable is satisfied by a device reachable through a
// console or terminal server's bare TCP byte pipe: a Digi, Opengear,
// Lantronix, Perle, or Avocent/Cyclades unit proxying one serial line as
// one TCP port with ZERO framing, ZERO authentication, and ZERO
// encryption at the protocol level. There is no line control at all -
// no baud rate, no DTR/RTS, no break signal - which is the entire
// difference from RFC2217Capable below.
type RawPassthroughCapable interface {
	// RawPassthroughHost returns the console server's address.
	RawPassthroughHost() string

	// RawPassthroughPort returns the console server's TCP port for this
	// line.
	RawPassthroughPort() int
}

// RFC2217Capable is satisfied by a device reachable through a console
// server speaking RFC 2217 (the Telnet Com Port Control Option): a real
// control channel layered onto a Telnet session, negotiating baud rate,
// DTR/RTS, and break the way a locally attached serial line would offer
// them directly. This is a materially different claim about a target
// than RawPassthroughCapable's bare byte pipe, so it is a genuine
// sibling capability rather than a boolean flag on it - a flag would let
// capability.Resolves answer a control-line question ("can this device's
// baud rate be changed") "yes" for a target that can only move bytes.
//
// RawPassthroughCapable and RFC2217Capable are deliberately NOT nested
// under one shared parent the way JunosCapable and AristaEOSCapable sit
// under NetworkCLICapable (capabilities_network.go): that grouping exists
// because both genuinely share one method, CLIPrompt(). These two share
// no comparable accessor (one has nothing to configure at all, the other
// carries a full serialline.Config), so inventing an empty marker parent
// just to mirror that precedent's shape would assert a shared truth that
// does not exist, the same
// unchecked-vocabulary failure CatalystAPICapable's own doc comment
// warns against, just from the opposite direction.
type RFC2217Capable interface {
	// RFC2217Host returns the console server's address.
	RFC2217Host() string

	// RFC2217Port returns the console server's TCP port for this line.
	RFC2217Port() int

	// RFC2217Line returns the line configuration negotiated over the
	// control channel - the same serialline.Config shape SerialCapable
	// uses for a local line, since RFC 2217 is, in substance, remote
	// control of the identical baud/parity/stop-bit settings.
	RFC2217Line() serialline.Config
}

// TelnetCapable is satisfied by a device reachable over a bare
// interactive Telnet session: genuinely ancient gear with no SSH at all.
// Ansible ships ansible.netcommon.telnet for exactly this reason, and its
// own documentation states the reason plainly: it exists "mostly to be
// used for enabling ssh on devices that only have telnet enabled by
// default." Telnet sends credentials in cleartext; that fact belongs in
// an operator's face (an explicit, loud opt-in on the binding), never in
// a footnote.
type TelnetCapable interface {
	// TelnetHost returns the device's address.
	TelnetHost() string

	// TelnetPort returns the device's Telnet port.
	TelnetPort() int
}

func init() {
	Register(Descriptor{
		Name:   NameSerial,
		Assert: func(item any) bool { _, ok := item.(SerialCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameRawPassthrough,
		Assert: func(item any) bool { _, ok := item.(RawPassthroughCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameRFC2217,
		Assert: func(item any) bool { _, ok := item.(RFC2217Capable); return ok },
	})
	Register(Descriptor{
		Name:   NameTelnet,
		Assert: func(item any) bool { _, ok := item.(TelnetCapable); return ok },
	})
}

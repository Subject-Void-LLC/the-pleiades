// Package serialline holds the typed vocabulary for a serial line's
// identity and configuration: a device identifier and a line's baud
// rate, data bits, parity, and stop bits. It exists because neither
// obvious home for these types is legal. pkg/capability needs Device and
// Config as SerialCapable's own accessor return types, but pkg/capability
// cannot import internal/transport (internal/archtest's
// TestPkgNeverImportsInternal forbids it). internal/transport/serial (the
// eventual Adapter) needs the same types as its own input, but
// internal/transport must not import pkg/capability either: that
// package's own doc comment states it "knows nothing about inventory
// devices, capabilities, or any specific wire protocol," and that
// decoupling is what lets Phase 16's runner mesh reuse it. This leaf
// package, importable from both sides, is the resolution — the same
// role pkg/credential-shaped leaf packages already play elsewhere in
// this module.
//
// Named serialline, not serial, so it is never confused with
// internal/transport/serial, the package that actually dials a line.
package serialline

import "fmt"

// Device identifies a serial port by whatever name the host operating
// system assigns it: "/dev/ttyUSB0" on Linux, "/dev/tty.usbserial-*" on
// macOS, "COM3" on Windows. It is OPAQUE and must never be parsed,
// joined, or validated as a POSIX path: "COM3" has no slashes and is not
// a filesystem path in the sense path/filepath means, and even the
// POSIX names are a stable identifier the operating system assigns, not
// a path this platform should construct or manipulate.
type Device string

// BaudRate is a serial line's signaling rate in bits per second (e.g.
// 9600, 115200). A named integer type, not a bare int, so a device
// property misread as a port number cannot silently become a line rate.
type BaudRate int

// Parity is a serial line's parity checking mode.
type Parity int

// The five parity modes every serial line configuration in this
// codebase may name.
const (
	ParityNone Parity = iota
	ParityOdd
	ParityEven
	ParityMark
	ParitySpace
)

// Valid reports whether p is one of the five parity modes this package
// knows.
func (p Parity) Valid() bool {
	return p >= ParityNone && p <= ParitySpace
}

// String renders p the way an operator would name it, for an error
// message or a logged configuration.
func (p Parity) String() string {
	switch p {
	case ParityNone:
		return "none"
	case ParityOdd:
		return "odd"
	case ParityEven:
		return "even"
	case ParityMark:
		return "mark"
	case ParitySpace:
		return "space"
	default:
		return fmt.Sprintf("Parity(%d)", int(p))
	}
}

// StopBits is the number of stop bits a serial line uses per frame.
type StopBits int

// The three stop-bit counts every serial line configuration in this
// codebase may name.
const (
	StopBitsOne StopBits = iota
	StopBitsOnePointFive
	StopBitsTwo
)

// Valid reports whether s is one of the three stop-bit counts this
// package knows.
func (s StopBits) Valid() bool {
	return s >= StopBitsOne && s <= StopBitsTwo
}

// String renders s the way an operator would name it, for an error
// message or a logged configuration.
func (s StopBits) String() string {
	switch s {
	case StopBitsOne:
		return "1"
	case StopBitsOnePointFive:
		return "1.5"
	case StopBitsTwo:
		return "2"
	default:
		return fmt.Sprintf("StopBits(%d)", int(s))
	}
}

// Config is a serial line's full configuration: everything needed to
// open a port and have both ends agree on how to interpret the bytes
// crossing it.
//
// There is deliberately no flow-control field. go.bug.st/serial, the
// library internal/transport/serial adopts, has none either: its own
// Mode struct offers only per-line SetDTR/SetRTS plus a read-only
// GetModemStatusBits, and cannot set RTS/CTS or XON/XOFF flow control at
// all. Shipping a configuration field the adapter would silently ignore
// is worse than not offering it, because an operator would configure it
// and believe it took effect.
type Config struct {
	// BaudRate is the line's signaling rate.
	BaudRate BaudRate

	// DataBits is the number of data bits per frame (commonly 7 or 8).
	// Deliberately a plain int, unlike BaudRate: no fixed vocabulary of
	// named values fits it the way Parity and StopBits have one.
	DataBits int

	// Parity is the line's parity checking mode.
	Parity Parity

	// StopBits is the number of stop bits per frame.
	StopBits StopBits
}

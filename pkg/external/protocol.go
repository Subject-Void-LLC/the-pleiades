// Package external: the protocol versions this build speaks, and how a
// program and The Pleiades agree on one.
//
// A program built against one release must keep loading after The Pleiades
// moves to the next protocol, or every external Collection ever built is
// stranded by the first bump. So a program says the range it speaks, The
// Pleiades says the versions it speaks (Protocols), and the loader takes the
// highest version both speak. The Pleiades speaks the newest version and the
// one before it, the one before kept at least one minor release after the
// newest goes final, and never dropped in a patch release.
// docs/13-releases-and-stability.md states the contract.
package external

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ProtocolStatus is where one protocol version stands in this build.
type ProtocolStatus string

const (
	// ProtocolSupported is a version this build speaks with no warning.
	ProtocolSupported ProtocolStatus = "supported"
	// ProtocolDeprecated is a version this build still speaks, warning once
	// per program each time programs are loaded (each command, each Runner
	// start), and that a named later release removes.
	ProtocolDeprecated ProtocolStatus = "deprecated"
)

// Protocol is one protocol version this build speaks.
type Protocol struct {
	// Version is the protocol version.
	Version int `json:"version"`
	// Status says whether it is supported or deprecated.
	Status ProtocolStatus `json:"status"`
	// RemovedIn names the release that stops speaking a deprecated version.
	RemovedIn string `json:"removed_in,omitempty"`
}

// Protocols is every protocol version this build speaks, oldest first. It is
// data: the loader negotiates from it, a program built with this package
// advertises it, and `pleiades version`, `runner version`, the Controller's
// /.well-known and `pleiades collection list` all print it.
//
// A patch release never removes or deprecates an entry here; only a minor
// release does, and deprecating one names the release that removes it.
var Protocols = []Protocol{
	{Version: 1, Status: ProtocolSupported},
}

// ErrNoCommonProtocol is Negotiate's refusal of a program that speaks no
// version this build does, so a caller can tell an unsupported program from
// one that could not be read at all.
var ErrNoCommonProtocol = errors.New("no protocol version in common")

// spokenRange is the lowest and highest version in Protocols.
func spokenRange() (lowest, highest int) {
	lowest, highest = Protocols[0].Version, Protocols[0].Version
	for _, p := range Protocols {
		lowest, highest = min(lowest, p.Version), max(highest, p.Version)
	}
	return lowest, highest
}

// Range is the protocol range a Description claims: ProtocolMin and
// ProtocolMax when it names them, and the single Protocol otherwise, which is
// what every program built before ranges existed prints.
func (d Description) Range() (lowest, highest int) {
	lowest, highest = d.ProtocolMin, d.ProtocolMax
	if lowest == 0 {
		lowest = d.Protocol
	}
	if highest == 0 {
		highest = d.Protocol
	}
	return lowest, highest
}

// Negotiate picks the protocol version a program speaking lowest..highest and
// this build will use: the highest version both speak. It refuses an empty or
// inverted range, and a range sharing no version with this build, naming the
// versions on each side.
func Negotiate(lowest, highest int) (Protocol, error) {
	switch {
	case lowest < 0 || highest < 0:
		return Protocol{}, fmt.Errorf("describe names a negative protocol version (%d to %d)", lowest, highest)
	case lowest == 0 || highest == 0:
		return Protocol{}, fmt.Errorf("describe names no protocol version (protocol %d to %d)", lowest, highest)
	case lowest > highest:
		return Protocol{}, fmt.Errorf("describe names an inverted protocol range, %d to %d", lowest, highest)
	}
	best := Protocol{}
	for _, p := range Protocols {
		if p.Version >= lowest && p.Version <= highest && p.Version > best.Version {
			best = p
		}
	}
	if best.Version == 0 {
		return Protocol{}, fmt.Errorf("%w: describe speaks protocol %s, and this build speaks protocol %s; rebuild the program against a release that speaks one of these",
			ErrNoCommonProtocol, spanText(lowest, highest), SpokenText())
	}
	return best, nil
}

// SpokenText names the versions this build speaks, for a message.
func SpokenText() string {
	parts := make([]string, 0, len(Protocols))
	for _, p := range Protocols {
		text := strconv.Itoa(p.Version)
		if p.Status == ProtocolDeprecated {
			text += " (deprecated, removed in " + p.RemovedIn + ")"
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, ", ")
}

// checkRequestProtocol refuses an invoke asking for a protocol version this
// build does not speak. Zero is a request from a loader that predates the
// field.
func checkRequestProtocol(v int) error {
	if v == 0 {
		return nil
	}
	for _, p := range Protocols {
		if p.Version == v {
			return nil
		}
	}
	return fmt.Errorf("the request asks for protocol %d, and this program speaks protocol %s", v, SpokenText())
}

// spanText writes a range as "1" or "2 to 3".
func spanText(lowest, highest int) string {
	if lowest == highest {
		return strconv.Itoa(lowest)
	}
	return fmt.Sprintf("%d to %d", lowest, highest)
}

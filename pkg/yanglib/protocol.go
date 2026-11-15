// Package yanglib: the modules a protocol's server lists for itself, which
// the yang.* facts leave out (owner decision 2026-11-15: yang.* is
// protocol-neutral). A device's YANG library, read over NETCONF and over
// RESTCONF at one moment, lists the same data models plus each protocol's
// own modules, so a module set that kept them would read as two sets for one
// device and as drift whenever the protocol changed. The list is measured,
// never guessed from a name: each entry is a module seen in one protocol's
// library and not the other's on a real device, named with where and when.
package yanglib

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// The protocols a library is read over.
const (
	ProtocolNETCONF  = "netconf"
	ProtocolRESTCONF = "restconf"
)

// ProtocolModule is one module a protocol's server lists for itself.
type ProtocolModule struct {
	// Name is the module's name; a revision is not matched, since a server
	// that moves to a new revision of its own module lists it no less.
	Name string
	// Protocol is the protocol whose server lists it.
	Protocol string
	// Measured says on which device, software and date the module was seen
	// in that protocol's library and not in the other's.
	Measured string
}

// measuredCatalyst8000V is where the first entries were measured: the set
// difference between one router's two libraries read minutes apart.
const measuredCatalyst8000V = "Cisco Catalyst 8000V, IOS XE 17.15.1a, 2026-11-15"

// protocolModules are every module known to be a protocol's own. A module
// both libraries list alike (ietf-netconf-monitoring on IOS XE) is a data
// model to the fact store and stays in yang.*: it does not break agreement,
// and moving it would be a guess.
var protocolModules = []ProtocolModule{
	{Name: "ietf-netconf", Protocol: ProtocolNETCONF, Measured: measuredCatalyst8000V},
	{Name: "ietf-netconf-with-defaults", Protocol: ProtocolNETCONF, Measured: measuredCatalyst8000V},
	{Name: "tailf-netconf-extensions", Protocol: ProtocolNETCONF, Measured: measuredCatalyst8000V},
	{Name: "tailf-rest-query", Protocol: ProtocolRESTCONF, Measured: measuredCatalyst8000V},
}

// DigestRule names the rule ModulesDigest is computed under: which modules
// it leaves out as the protocols' own. It is "protocol-modules-" and the first
// 12 hex digits of the SHA-256 of the list, each entry "protocol:name" on a
// line of its own, sorted, so it is short, the same on every build holding
// the same list, and different on any build whose list differs. A reader
// compares two yang.modules_digest values only when their rules are equal:
// a digest changes with the list as surely as with the device, and a fleet
// on two releases otherwise reads every device as drifted.
func DigestRule() string {
	lines := make([]string, 0, len(protocolModules))
	for _, m := range protocolModules {
		lines = append(lines, m.Protocol+":"+m.Name)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	return "protocol-modules-" + hex.EncodeToString(sum[:])[:12]
}

// ProtocolModules returns every module known to be a protocol's own, a copy.
func ProtocolModules() []ProtocolModule {
	return append([]ProtocolModule(nil), protocolModules...)
}

// isProtocolModule reports whether name is on the list, for either protocol:
// a module named on either is left out of the data models over both, so the
// two protocols' module sets agree whichever of them lists it.
func isProtocolModule(name string) bool {
	for _, m := range protocolModules {
		if m.Name == name {
			return true
		}
	}
	return false
}

// DataModules returns d's modules but the protocols' own: the device's data
// models, the same whichever protocol read them. The yang.* facts are built
// from them.
func (d Discovery) DataModules() []Module {
	out := make([]Module, 0, len(d.Modules))
	for _, m := range d.Modules {
		if !isProtocolModule(m.Name) {
			out = append(out, m)
		}
	}
	return out
}

// OwnModules returns the modules d's library listed that are a protocol's
// own (on the list for either protocol), in Modules' order: what the
// protocol whose read d is records in its own family.
func (d Discovery) OwnModules() []Module {
	var out []Module
	for _, m := range d.Modules {
		if isProtocolModule(m.Name) {
			out = append(out, m)
		}
	}
	return out
}

// Package yanglib is what a client learns about a YANG server's schema,
// whichever protocol it asked over: the modules the server implements, the
// datastores it offers, and how far that list can be trusted. NETCONF
// (pkg/netconf) and RESTCONF (pkg/restconf) read the same YANG library (RFC
// 8525, or RFC 7895's modules-state from an older server), so the model, the
// library parser, the refusal rules and the digest live here once, and a
// device read over either protocol records the same yang.* facts.
//
// Every bound here refuses one entry whole rather than cutting it short: an
// entry cut short names a module that does not exist.
package yanglib

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Completeness says how far a Discovery's module list can be trusted to be
// every module the server implements.
type Completeness string

const (
	// Complete is a list read from the server's YANG library, or from the
	// NETCONF hello of a server with no library (RFC 6020 requires such a
	// server to list every module there).
	Complete Completeness = "complete"
	// Partial is a list known to be missing modules: the hello of a server
	// with a library that could not be read, a library that came back empty
	// (an account the server's access control hides it from), or a list
	// that dropped a malformed entry.
	Partial Completeness = "partial"
	// Unknown is a server that named no modules at all.
	Unknown Completeness = "unknown"
)

// The bounds on what a Discovery keeps.
const (
	// MaxModules is the most modules a Discovery keeps; past it each further
	// entry is counted as dropped. A real Catalyst 8000V implements 559.
	MaxModules = 20000
	// MaxNamespaceBytes bounds a module's namespace and a protocol
	// capability.
	MaxNamespaceBytes = 1024
	// MaxListEntries bounds one module's features and deviations.
	MaxListEntries = 512
)

// maxDroppedSamples is how many dropped entries a Discovery names.
const maxDroppedSamples = 5

// Module is one YANG module a server implements.
type Module struct {
	// Name is the module's name, such as Cisco-IOS-XE-native.
	Name string `json:"name"`
	// Namespace is the module's XML namespace, the value a document's
	// xmlns names it by.
	Namespace string `json:"namespace"`
	// Revision is the module's revision date, absent when the server named
	// none.
	Revision string `json:"revision,omitempty"`
	// Features are the module's features the server supports, sorted.
	Features []string `json:"features,omitempty"`
	// Deviations are the modules that deviate this one, sorted.
	Deviations []string `json:"deviations,omitempty"`
}

// Discovery is what one client learned about its server.
type Discovery struct {
	// Protocol is every protocol capability the server advertised, with its
	// parameters, sorted: for NETCONF each hello capability that names no
	// YANG module (RFC 6241's own under urn:ietf:params:netconf: and a
	// vendor's, such as Tail-f's http://tail-f.com/ns/netconf/actions/1.0 on
	// IOS XE); for RESTCONF each capability URI ietf-restconf-monitoring
	// lists (RFC 8040 section 9.1).
	Protocol []string
	// Modules is every implemented module, sorted by name then revision.
	Modules []Module
	// Source is where Modules came from: "hello" (NETCONF's), or
	// "yang-library:1.1" (RFC 8525) or "yang-library:1.0" (RFC 7895).
	Source string
	// Completeness says how far Modules can be trusted to be complete.
	Completeness Completeness
	// Datastores are the datastores the client can use, sorted. NETCONF
	// derives running, candidate and startup from its hello; both protocols
	// add the datastores an NMDA server's library lists (RFC 8342, RFC 8526
	// and RFC 8527), its operational and intended among them.
	Datastores []string
	// ContentID is the YANG library's content-id (RFC 8525) or module-set-id
	// (RFC 7895), a hint the server may regenerate; empty when the server
	// gives none. The module set's own identity is ModulesDigest.
	ContentID string
	// Dropped counts the entries refused as malformed or too long, module
	// entries and protocol capabilities alike, for a warning.
	Dropped int
	// ModulesDropped counts the module entries alone that were refused from
	// the list Modules came from, which is what makes the list Partial: a
	// refused protocol capability says nothing about the modules.
	ModulesDropped int
	// DroppedSamples are up to five of those entries, each quoted (so no
	// control character reaches a log) and cut to 120 bytes, for a warning to
	// name.
	DroppedSamples []string
	// LibraryError says why the YANG library could not be read, when it
	// could not.
	LibraryError string
}

// Drop counts one refused entry and keeps a quoted sample of it.
func (d *Discovery) Drop(entry string) {
	d.Dropped++
	if len(d.DroppedSamples) < maxDroppedSamples {
		// The sample is cut before it is quoted, so a long entry still
		// names its start; the quoting shows any control character as an
		// escape rather than acting on it.
		if len(entry) > 120 {
			entry = entry[:120]
		}
		d.DroppedSamples = append(d.DroppedSamples, fmt.Sprintf("%q", entry))
	}
}

// dropModule counts one refused module entry, which Completeness reads.
func (d *Discovery) dropModule(entry string) {
	d.ModulesDropped++
	d.Drop(entry)
}

// ModulesDigest is the SHA-256 (hex) of the device's data models in their
// canonical form (DataModules, sorted, as JSON): the protocols' own modules
// are left out, so one device read over NETCONF and over RESTCONF gives one
// digest, and two servers implementing the same models give the same digest
// whatever order they listed them in.
func (d Discovery) ModulesDigest() string {
	raw, err := json.Marshal(d.DataModules())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ServesModule reports whether the server implements the module named name,
// with known false when the module list is not Complete, as Serves does: a
// RESTCONF path names its modules by name where a NETCONF document names
// them by namespace.
func (d Discovery) ServesModule(name string) (served, known bool) {
	if d.Completeness != Complete {
		return false, false
	}
	for _, m := range d.Modules {
		if m.Name == name {
			return true, true
		}
	}
	return false, true
}

// Serves reports whether the server implements a module in namespace. known
// is false when the module list is not Complete, so no answer can be given:
// a caller refuses a namespace only when known is true and served is false.
func (d Discovery) Serves(namespace string) (served, known bool) {
	if d.Completeness != Complete {
		return false, false
	}
	for _, m := range d.Modules {
		if m.Namespace == namespace {
			return true, true
		}
	}
	return false, true
}

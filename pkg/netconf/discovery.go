// Package netconf: what a session learns about its server when it opens
// (owner decision 2026-11-14). Every session builds one Discovery: the
// protocol capabilities the hello advertised, the YANG modules the server
// implements, and the datastores it offers. The modules come from the hello
// and, when the server advertises a YANG library, from that library, which is
// authoritative: RFC 7950 section 5.6.4 lets a YANG 1.1 server leave its
// modules out of the hello. Measured on a Cisco Catalyst 8000V (IOS XE
// 17.15.1a): turning on its candidate datastore withdrew :writable-running
// from the hello, so the datastore a task writes is chosen from what the
// session discovered, never from what an earlier session or a stored fact
// said.
package netconf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// More capability URNs a Discovery names, beside the ones in netconf.go.
const (
	CapabilityStartup           = "urn:ietf:params:netconf:capability:startup:1.0"
	CapabilityValidate11        = "urn:ietf:params:netconf:capability:validate:1.1"
	CapabilityConfirmedCommit11 = "urn:ietf:params:netconf:capability:confirmed-commit:1.1"
	CapabilityYANGLibrary10     = "urn:ietf:params:netconf:capability:yang-library:1.0"
	CapabilityYANGLibrary11     = "urn:ietf:params:netconf:capability:yang-library:1.1"
)

// protocolCapabilityPrefix leads every capability RFC 6241 and its successors
// define; anything else in a hello names a YANG module.
const protocolCapabilityPrefix = "urn:ietf:params:netconf:"

// Completeness says how far a Discovery's module list can be trusted to be
// every module the server implements.
type Completeness string

const (
	// Complete is a list read from the server's YANG library, or from the
	// hello of a server with no library (RFC 6020 requires such a server to
	// list every module there).
	Complete Completeness = "complete"
	// Partial is a list known to be missing modules: the hello of a server
	// with a library that could not be read, or a library that came back
	// empty (an account the server's access control hides it from).
	Partial Completeness = "partial"
	// Unknown is a server that named no modules at all.
	Unknown Completeness = "unknown"
)

// The bounds on what a Discovery keeps, each a refusal of one entry rather
// than a truncation: an entry cut short names a module that does not exist.
const (
	maxModules        = 20000
	maxNamespaceBytes = 1024
	maxListEntries    = 512
)

// maxDroppedSamples is how many dropped entries a Discovery names.
const maxDroppedSamples = 5

// drop counts one refused entry and keeps a quoted sample of it.
func (d *Discovery) drop(entry string) {
	d.Dropped++
	if len(d.DroppedSamples) < maxDroppedSamples {
		if len(entry) > 120 {
			entry = entry[:120]
		}
		d.DroppedSamples = append(d.DroppedSamples, fmt.Sprintf("%q", entry))
	}
}

// yangIdentifier is RFC 7950 section 6.2's identifier grammar, which a module,
// feature or deviation name must match before it is kept.
var yangIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,254}$`)

// yangRevision is a revision date, RFC 7950 section 7.1.9.
var yangRevision = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

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

// Discovery is what one session learned about its server.
type Discovery struct {
	// Protocol is every capability the hello advertised that names no YANG
	// module, with its parameters, sorted: RFC 6241's own (each URN under
	// urn:ietf:params:netconf:) and a vendor's (Tail-f's
	// http://tail-f.com/ns/netconf/actions/1.0 on IOS XE).
	Protocol []string
	// Modules is every implemented module, sorted by name then revision.
	Modules []Module
	// Source is where Modules came from: "hello", "yang-library:1.1" or
	// "yang-library:1.0".
	Source string
	// Completeness says how far Modules can be trusted to be complete.
	Completeness Completeness
	// Datastores are the configuration datastores the session can use,
	// sorted: running always, and candidate and startup when advertised.
	// It is derived from the capabilities, which every server has; an NMDA
	// server's library also lists its operational and intended datastores,
	// which are added when it was read.
	Datastores []string
	// ContentID is the YANG library's content-id from the hello's
	// yang-library:1.1 capability, a hint the server may regenerate; empty
	// when the server advertises none. The module set's own identity is
	// ModulesDigest.
	ContentID string
	// Dropped counts the entries refused as malformed or too long.
	Dropped int
	// DroppedSamples are up to maxDroppedSamples of those entries, each
	// quoted (so no control character reaches a log) and cut to 120
	// bytes, for a warning to name.
	DroppedSamples []string
	// LibraryError says why the YANG library could not be read, when the
	// server advertises one and it could not.
	LibraryError string
}

// ModulesDigest is the SHA-256 (hex) of the module set in its canonical form
// (Modules, sorted, as JSON). Two servers implementing the same modules give
// the same digest whatever order they listed them in.
func (d Discovery) ModulesDigest() string {
	raw, err := json.Marshal(d.Modules)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
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

// discoverFromHello builds the Discovery a hello alone gives.
func discoverFromHello(capabilities []string) Discovery {
	d := Discovery{Source: "hello"}
	var modules []Module
	library := false
	for _, c := range capabilities {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if m, ok := moduleFromCapability(c); ok && !strings.HasPrefix(c, protocolCapabilityPrefix) {
			modules = append(modules, m)
			continue
		}
		if !printable(c) || len(c) > maxNamespaceBytes {
			d.drop(c)
			continue
		}
		d.Protocol = append(d.Protocol, c)
		base, _, _ := strings.Cut(c, "?")
		if base == CapabilityYANGLibrary10 || base == CapabilityYANGLibrary11 {
			library = true
		}
	}
	sort.Strings(d.Protocol)
	d.Modules = keepModules(modules, &d)
	switch {
	case len(d.Modules) == 0 && !library:
		d.Completeness = Unknown
	case library || d.Dropped > 0:
		// A list known to miss an entry, one malformed or over a bound,
		// is not every module the server implements.
		d.Completeness = Partial
	default:
		d.Completeness = Complete
	}
	d.ContentID = validID(capabilityParam(capabilities, CapabilityYANGLibrary11, "content-id"))
	d.Datastores = datastoresFrom(d.Protocol, nil)
	return d
}

// moduleFromCapability reads a module capability, RFC 6020 section 5.6.4's
// "<namespace>?module=<name>&revision=<date>&features=a,b&deviations=c,d".
// A capability with no module parameter is not a module, and ok is false; one
// with a module parameter is, however malformed its fields, for keepModules
// to judge.
func moduleFromCapability(capability string) (Module, bool) {
	namespace, query, found := strings.Cut(strings.TrimSpace(capability), "?")
	if !found {
		return Module{}, false
	}
	values, err := url.ParseQuery(strings.ReplaceAll(query, "&amp;", "&"))
	if err != nil || !values.Has("module") {
		return Module{}, false
	}
	return Module{
		Name:       strings.TrimSpace(values.Get("module")),
		Namespace:  namespace,
		Revision:   strings.TrimSpace(values.Get("revision")),
		Features:   splitList(values.Get("features")),
		Deviations: splitList(values.Get("deviations")),
	}, true
}

// keepModules is modules with every malformed entry refused (counted on d),
// each feature and deviation list checked the same way, duplicates removed,
// sorted by name then revision.
func keepModules(modules []Module, d *Discovery) []Module {
	seen := map[string]bool{}
	out := make([]Module, 0, len(modules))
	for _, m := range modules {
		if len(out) == maxModules {
			d.drop(m.Name)
			continue
		}
		if !yangIdentifier.MatchString(m.Name) || (m.Revision != "" && !yangRevision.MatchString(m.Revision)) ||
			m.Namespace == "" || len(m.Namespace) > maxNamespaceBytes || !printable(m.Namespace) ||
			len(m.Features) > maxListEntries || len(m.Deviations) > maxListEntries {
			d.drop(m.Name + "@" + m.Revision + " " + m.Namespace)
			continue
		}
		m.Features, m.Deviations = identifiers(m.Features), identifiers(m.Deviations)
		key := m.Name + "@" + m.Revision
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}

// identifiers is names with every one that is not a YANG identifier left out,
// sorted, without duplicates; nil when none is left.
func identifiers(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if yangIdentifier.MatchString(n) && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// splitList is a comma-separated list, trimmed, without empty entries.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// printable reports whether s holds no control or invisible formatting
// character, so it can reach a log, a terminal or a fact store as it is.
func printable(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// validID is id when it is a short printable token, else empty.
func validID(id string) string {
	if id == "" || len(id) > 256 || !printable(id) || strings.ContainsAny(id, " \t") {
		return ""
	}
	return id
}

// capabilityParam is the value of one query parameter of the advertised
// capability whose URN (before its "?") is urn, empty when the server did not
// advertise it or named no such parameter.
func capabilityParam(capabilities []string, urn, param string) string {
	for _, c := range capabilities {
		base, query, found := strings.Cut(strings.TrimSpace(c), "?")
		if base != urn || !found {
			continue
		}
		values, err := url.ParseQuery(strings.ReplaceAll(query, "&amp;", "&"))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(values.Get(param))
	}
	return ""
}

// datastoresFrom is the datastores the capabilities give (running always,
// candidate and startup when advertised) plus those a YANG library named,
// sorted.
func datastoresFrom(protocol []string, library []string) []string {
	set := map[string]bool{string(Running): true}
	for _, c := range protocol {
		base, _, _ := strings.Cut(c, "?")
		switch base {
		case CapabilityCandidate:
			set[string(Candidate)] = true
		case CapabilityStartup:
			set[string(Startup)] = true
		}
	}
	for _, name := range library {
		if yangIdentifier.MatchString(name) {
			set[name] = true
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

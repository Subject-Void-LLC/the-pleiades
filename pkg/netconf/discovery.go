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
// said. The model, the library parser and the refusal rules are
// pkg/yanglib's, shared with RESTCONF; the hello is NETCONF's own.
package netconf

import (
	"net/url"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
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

// The discovery model is pkg/yanglib's, which RESTCONF shares; these names
// keep this package's API as it was.
type (
	// Completeness is yanglib.Completeness.
	Completeness = yanglib.Completeness
	// Module is yanglib.Module.
	Module = yanglib.Module
	// Discovery is yanglib.Discovery: what one session learned about its
	// server.
	Discovery = yanglib.Discovery
)

// The completeness values, yanglib's.
const (
	Complete = yanglib.Complete
	Partial  = yanglib.Partial
	Unknown  = yanglib.Unknown
)

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
		if !yanglib.Printable(c) || len(c) > yanglib.MaxNamespaceBytes {
			d.Drop(c)
			continue
		}
		d.Protocol = append(d.Protocol, c)
		base, _, _ := strings.Cut(c, "?")
		if base == CapabilityYANGLibrary10 || base == CapabilityYANGLibrary11 {
			library = true
		}
	}
	sort.Strings(d.Protocol)
	d.Modules = yanglib.KeepModules(modules, &d)
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
	d.ContentID = yanglib.ValidID(capabilityParam(capabilities, CapabilityYANGLibrary11, "content-id"))
	d.Datastores = datastoresFrom(d.Protocol, nil)
	return d
}

// moduleFromCapability reads a module capability, RFC 6020 section 5.6.4's
// "<namespace>?module=<name>&revision=<date>&features=a,b&deviations=c,d".
// A capability with no module parameter is not a module, and ok is false; one
// with a module parameter is, however malformed its fields, for
// yanglib.KeepModules to judge.
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
		if yanglib.IsIdentifier(name) {
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

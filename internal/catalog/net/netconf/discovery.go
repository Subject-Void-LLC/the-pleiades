// Package netconf: what a session discovered, recorded on the device and held
// against the document before it is sent (owner decision 2026-11-14).
package netconf

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangfacts"
)

// The facts every NETCONF session records about its device. The module set
// belongs to the device's management plane rather than to NETCONF (RESTCONF
// on the same device serves the same library), so it is the yang. family,
// built by pkg/yangfacts for every protocol; the capabilities and
// datastores are NETCONF's own.
const (
	factCapabilities = "netconf.capabilities"
	factDatastores   = "netconf.datastores"
	factModules      = yangfacts.FactModules
	factModuleCount  = yangfacts.FactModuleCount
	factModuleDigest = yangfacts.FactModuleDigest
	factCompleteness = yangfacts.FactCompleteness
	factContentID    = yangfacts.FactContentID
)

// discoveryFactKeys are every fact a session may record, for the manifest.
var discoveryFactKeys = append([]string{factCapabilities, factDatastores}, yangfacts.FactKeys...)

// netconfSource is where a NETCONF session reads the yang.* facts from.
var netconfSource = yangfacts.Source{
	Modules:       "from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise",
	Completeness:  "How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete.",
	ContentID:     "when the device advertises yang-library:1.1",
	ContentIDFrom: "from the hello",
	Fallback:      "the hello's, ",
}

// discoveryReturns documents the facts, one return each.
func discoveryReturns() []collection.ReturnField {
	const when = "on every session, as a stat; as a fact only when its host key was verified"
	return append([]collection.ReturnField{
		{Name: factCapabilities, Type: "list of string", Returned: when, Description: "Every capability the device's hello advertised that names no YANG module (RFC 6241's own and a vendor's), with its parameters but the volatile content-id and module-set-id, which yang.content_id holds, sorted; the YANG module capabilities are in yang.modules instead. Left out, with a warning, when it does not fit in 60 KiB. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change."},
		{Name: factDatastores, Type: "list of string", Returned: when, Description: "The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended)."},
	}, yangfacts.Returns(when, netconfSource)...)
}

// recordDiscovery records d as facts and stats on the device, unless the
// session's host key was not verified: a machine in the middle can strip a
// capability or invent a module, and what it says is not kept. It returns the
// warnings the task should carry.
func recordDiscovery(rc sdk.RunbookContext, params map[string]any, d netconf.Discovery) ([]string, error) {
	capabilities, err := capabilitiesValue(d.Protocol)
	if err != nil {
		return nil, err
	}
	values, schemaWarnings, err := yangfacts.Values(d, netconfSource)
	if err != nil {
		return nil, err
	}
	values[factCapabilities] = capabilities
	values[factDatastores] = yangfacts.NonNil(d.Datastores)
	var warnings []string
	if len(capabilities) == 0 && len(d.Protocol) > 0 {
		warnings = append(warnings, fmt.Sprintf("the device advertises %d capabilities, more than %s holds in 60 KiB; left out", len(d.Protocol), factCapabilities))
	}
	warnings = append(warnings, schemaWarnings...)
	skip, err := sdk.BoolParamOr(params, sdk.ParamInsecureSkipHostKeyVerify, false)
	if err != nil {
		return nil, err
	}
	if err := yangfacts.Record(rc, discoveryFactKeys, values, !skip); err != nil {
		return nil, err
	}
	if skip {
		warnings = append(warnings, "the host key was not verified, so nothing this session discovered is recorded as a fact")
	}
	return warnings, nil
}

// volatileCapabilityParams are the parameters a capability carries that a
// device may regenerate with nothing changed: the YANG library's content-id
// and module-set-id. They are recorded in yang.content_id (volatile), so
// netconf.capabilities, a stable fact, leaves them out.
var volatileCapabilityParams = []string{"content-id", "module-set-id"}

// capabilitiesValue is protocol as netconf.capabilities records it: each
// capability without its volatile parameters, sorted, and empty when the list
// does not fit in yangfacts.MaxListFactBytes as JSON.
func capabilitiesValue(protocol []string) ([]string, error) {
	out := make([]string, 0, len(protocol))
	for _, c := range protocol {
		out = append(out, withoutVolatileParams(c))
	}
	sort.Strings(out)
	if !yangfacts.Fits(out) {
		return []string{}, nil
	}
	return out, nil
}

// withoutVolatileParams is capability without its content-id and
// module-set-id parameters, the others kept in their order.
func withoutVolatileParams(capability string) string {
	base, query, found := strings.Cut(capability, "?")
	if !found {
		return capability
	}
	var kept []string
	for _, part := range strings.Split(query, "&") {
		name, _, _ := strings.Cut(part, "=")
		if !slices.Contains(volatileCapabilityParams, name) {
			kept = append(kept, part)
		}
	}
	if len(kept) == 0 {
		return base
	}
	return base + "?" + strings.Join(kept, "&")
}

// baseNamespace is NETCONF's own namespace, which a document names for its
// nc:operation attributes and never as a module.
const baseNamespace = "urn:ietf:params:xml:ns:netconf:base:1.0"

// unservedNamespaces are the namespaces of content's top-level elements the
// device does not serve, when its module list is complete; none otherwise. An
// element with no namespace is left to the device, which may accept it (Junos
// does), and nothing deeper is checked: a mounted schema or a device tree
// below a top-level element is the device's to judge.
func unservedNamespaces(content string, d netconf.Discovery) ([]string, error) {
	if _, known := d.Serves(""); !known {
		return nil, nil
	}
	dec := xml.NewDecoder(strings.NewReader("<config>" + content + "</config>"))
	depth := 0
	seen := map[string]bool{}
	var out []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s is not well-formed XML: %w", paramContent, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth != 2 {
				continue
			}
			ns := t.Name.Space
			if ns == "" || ns == baseNamespace || seen[ns] {
				continue
			}
			seen[ns] = true
			if served, _ := d.Serves(ns); !served {
				out = append(out, ns)
			}
		case xml.EndElement:
			depth--
		}
	}
}

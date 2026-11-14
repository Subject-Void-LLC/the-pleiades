// Package netconf: what a session discovered, recorded on the device and held
// against the document before it is sent (owner decision 2026-11-14).
package netconf

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The facts every NETCONF session records about its device. The module set
// belongs to the device's management plane rather than to NETCONF (RESTCONF
// on the same device serves the same library), so it is the yang. family; the
// capabilities and datastores are NETCONF's own.
const (
	factCapabilities = "netconf.capabilities"
	factDatastores   = "netconf.datastores"
	factModules      = "yang.modules"
	factModuleCount  = "yang.module_count"
	factModuleDigest = "yang.modules_digest"
	factCompleteness = "yang.completeness"
	factContentID    = "yang.content_id"
)

// discoveryFactKeys are every fact a session may record, for the manifest.
var discoveryFactKeys = []string{
	factCapabilities, factDatastores, factModules, factModuleCount, factModuleDigest, factCompleteness, factContentID,
}

// maxModulesFactBytes is the most the module list may take as JSON and still
// be recorded, under the fact store's 64 KiB value bound with room for its own
// encoding. The list is each module's name@revision, which is what a fleet
// query asks of it (devices implementing Cisco-IOS-XE-native); the full
// entries, with namespaces, features and deviations, are what
// yang.modules_digest covers. A list past the bound is left out and the count
// and digest, always present, still are, so the fact never changes type.
// Measured on a Catalyst 8000V (IOS XE 17.15.1a): 559 modules as full entries
// passed 60 KiB, as name@revision they are about 20 KiB.
const maxModulesFactBytes = 60 << 10

// discoveryReturns documents the facts, one return each.
func discoveryReturns() []collection.ReturnField {
	const when = "on every session whose host key was verified"
	return []collection.ReturnField{
		{Name: factCapabilities, Type: "list of string", Returned: when, Description: "Every NETCONF protocol capability the device's hello advertised (each URN under urn:ietf:params:netconf:, with its parameters), sorted; the YANG module capabilities are in yang.modules instead. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change."},
		{Name: factDatastores, Type: "list of string", Returned: when, Description: "The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended)."},
		{Name: factModules, Type: "list of string", Returned: "when the list fits in 60 KiB as JSON", Description: "Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded."},
		{Name: factModuleCount, Type: "int", Returned: when, Description: "How many YANG modules the device implements, as yang.modules counts them."},
		{Name: factModuleDigest, Type: "string", Returned: when, Description: "The SHA-256 (hex) of the module set in its canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), so two devices implementing the same modules read alike and any change to the set reads as a change of this one value."},
		{Name: factCompleteness, Type: "string", Returned: when, Description: "How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete."},
		{Name: factContentID, Type: "string", Returned: "when the device advertises yang-library:1.1", Description: "The YANG library's content-id from the hello, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity."},
	}
}

// recordDiscovery records d as facts and stats on the device, unless the
// session's host key was not verified: a machine in the middle can strip a
// capability or invent a module, and what it says is not kept. It returns the
// warnings the task should carry.
func recordDiscovery(rc sdk.RunbookContext, params map[string]any, d netconf.Discovery) ([]string, error) {
	values := map[string]any{
		factCapabilities: nonNil(d.Protocol),
		factDatastores:   nonNil(d.Datastores),
		factModuleCount:  len(d.Modules),
		factModuleDigest: d.ModulesDigest(),
		factCompleteness: string(d.Completeness),
	}
	if d.ContentID != "" {
		values[factContentID] = d.ContentID
	}
	modules, err := modulesValue(d.Modules)
	if err != nil {
		return nil, err
	}
	if modules != nil {
		values[factModules] = modules
	}
	var warnings []string
	if modules == nil && len(d.Modules) > 0 {
		warnings = append(warnings, fmt.Sprintf("the device implements %d YANG modules, more than %s holds in 60 KiB; recorded by count and digest only", len(d.Modules), factModules))
	}
	if d.LibraryError != "" {
		warnings = append(warnings, "the device's YANG library was not read ("+d.LibraryError+"); the module list is the hello's, "+string(d.Completeness))
	}
	if d.Dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d malformed or oversized capability or module entries from the device were left out: %s", d.Dropped, strings.Join(d.DroppedSamples, ", ")))
	}
	skip, err := sdk.BoolParamOr(params, sdk.ParamInsecureSkipHostKeyVerify, false)
	if err != nil {
		return nil, err
	}
	for _, key := range discoveryFactKeys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if err := rc.SetStat(key, value); err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if err := rc.EmitFact(key, value); err != nil {
			return nil, err
		}
	}
	if skip {
		warnings = append(warnings, "the host key was not verified, so nothing this session discovered is recorded as a fact")
	}
	return warnings, nil
}

// modulesValue is modules as the fact's value, each name@revision (name alone
// for a module with no revision), nil when the list does not fit.
func modulesValue(modules []netconf.Module) ([]string, error) {
	out := make([]string, 0, len(modules))
	for _, m := range modules {
		if m.Revision == "" {
			out = append(out, m.Name)
		} else {
			out = append(out, m.Name+"@"+m.Revision)
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxModulesFactBytes {
		return nil, nil
	}
	return out, nil
}

// nonNil is list, or an empty list for nil, so a fact is always a list.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
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

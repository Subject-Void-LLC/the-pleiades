// Package yanglib: reading a server's YANG library (RFC 8525's yang-library,
// or RFC 7895's modules-state from an older server), the authoritative list
// of the modules it implements, in the XML encoding NETCONF and RESTCONF
// share (RFC 7950) and in RESTCONF's JSON encoding (RFC 7951).
package yanglib

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Namespace is the YANG library's namespace, which RFC 8525 and RFC 7895
// share.
const Namespace = "urn:ietf:params:xml:ns:yang:ietf-yang-library"

// The sources a library read names, the form it was read in.
const (
	SourceLibrary11 = "yang-library:1.1"
	SourceLibrary10 = "yang-library:1.0"
)

// ErrEmptyLibrary is a library that listed no module the rules keep.
var ErrEmptyLibrary = errors.New("the YANG library came back empty, as it does for an account the server's access control hides it from")

// Library is one read of a server's YANG library, before its entries are
// held to the refusal rules (Keep).
type Library struct {
	// Source is SourceLibrary11 for RFC 8525's yang-library and
	// SourceLibrary10 for RFC 7895's modules-state; empty when the answer
	// held neither.
	Source string
	// Modules is every implemented module the library listed, as it listed
	// it: RFC 7895's import-only modules are left out, as RFC 8525 lists
	// them apart.
	Modules []Module
	// Datastores are the datastore names RFC 8525's library lists, each
	// without its identityref prefix (ds:running is running).
	Datastores []string
	// ContentID is RFC 8525's content-id or RFC 7895's module-set-id, as
	// written.
	ContentID string
}

// moduleFields are the fields RFC 8525's and RFC 7895's module entries
// share, in either encoding.
type moduleFields struct {
	Name      string   `xml:"name" json:"name"`
	Revision  string   `xml:"revision" json:"revision"`
	Namespace string   `xml:"namespace" json:"namespace"`
	Features  []string `xml:"feature" json:"feature"`
}

// libraryModule is one implemented module of an RFC 8525 module set; its
// deviation entries name the deviating modules.
type libraryModule struct {
	moduleFields
	Deviations []string `xml:"deviation" json:"deviation"`
}

// stateModule is one module of RFC 7895's modules-state, whose deviations are
// a list of name and revision.
type stateModule struct {
	moduleFields
	Conformance string `xml:"conformance-type" json:"conformance-type"`
	Deviations  []struct {
		Name string `xml:"name" json:"name"`
	} `xml:"deviation" json:"deviation"`
}

// YANGLibrary is RFC 8525's yang-library container as either encoding decodes
// it. A NETCONF reply embeds it in its data element; a RESTCONF answer is it.
type YANGLibrary struct {
	ModuleSets []struct {
		Modules []libraryModule `xml:"module" json:"module"`
	} `xml:"module-set" json:"module-set"`
	Datastores []struct {
		Name string `xml:"name" json:"name"`
	} `xml:"datastore" json:"datastore"`
	ContentID string `xml:"content-id" json:"content-id"`
}

// ModulesState is RFC 7895's modules-state container as either encoding
// decodes it.
type ModulesState struct {
	ModuleSetID string        `xml:"module-set-id" json:"module-set-id"`
	Modules     []stateModule `xml:"module" json:"module"`
}

// Library is the read the yang-library container gives.
func (l *YANGLibrary) Library() Library {
	lib := Library{Source: SourceLibrary11, ContentID: strings.TrimSpace(l.ContentID)}
	for _, set := range l.ModuleSets {
		for _, m := range set.Modules {
			lib.Modules = append(lib.Modules, moduleOf(m.moduleFields, m.Deviations))
		}
	}
	for _, ds := range l.Datastores {
		// An identityref such as "ds:running" (XML) or
		// "ietf-datastores:running" (JSON); the name is after the prefix.
		name := strings.TrimSpace(ds.Name)
		if i := strings.LastIndexByte(name, ':'); i >= 0 {
			name = name[i+1:]
		}
		lib.Datastores = append(lib.Datastores, name)
	}
	return lib
}

// Library is the read the modules-state container gives: its implemented
// modules, an import-only one left out.
func (s *ModulesState) Library() Library {
	lib := Library{Source: SourceLibrary10, ContentID: strings.TrimSpace(s.ModuleSetID)}
	for _, m := range s.Modules {
		if strings.TrimSpace(m.Conformance) == "import" {
			continue
		}
		var deviations []string
		for _, d := range m.Deviations {
			deviations = append(deviations, d.Name)
		}
		lib.Modules = append(lib.Modules, moduleOf(m.moduleFields, deviations))
	}
	return lib
}

// moduleOf is a library entry as a Module, every field trimmed.
func moduleOf(f moduleFields, deviations []string) Module {
	return Module{
		Name:       strings.TrimSpace(f.Name),
		Namespace:  strings.TrimSpace(f.Namespace),
		Revision:   strings.TrimSpace(f.Revision),
		Features:   f.Features,
		Deviations: deviations,
	}
}

// ParseXML reads a document whose root element is the YANG library's
// yang-library or modules-state, in the library's namespace: the XML
// encoding of a RESTCONF answer for either resource. Any other root is an
// error, never an empty library.
func ParseXML(raw []byte) (Library, error) {
	root, err := rootElement(raw)
	if err != nil {
		return Library{}, fmt.Errorf("reading the YANG library: %w", err)
	}
	if root.Space != Namespace {
		return Library{}, fmt.Errorf("reading the YANG library: the answer's root element %q is not in the library's namespace", root.Local)
	}
	switch root.Local {
	case "yang-library":
		var l YANGLibrary
		if err := xml.Unmarshal(raw, &l); err != nil {
			return Library{}, fmt.Errorf("reading the YANG library: %w", err)
		}
		return l.Library(), nil
	case "modules-state":
		var s ModulesState
		if err := xml.Unmarshal(raw, &s); err != nil {
			return Library{}, fmt.Errorf("reading the YANG library: %w", err)
		}
		return s.Library(), nil
	}
	return Library{}, fmt.Errorf("reading the YANG library: the answer's root element %q is neither yang-library nor modules-state", root.Local)
}

// rootElement is the name of raw's first element, skipping the prolog.
func rootElement(raw []byte) (xml.Name, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return xml.Name{}, errors.New("the answer holds no element")
		}
		if err != nil {
			return xml.Name{}, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start.Name, nil
		}
	}
}

// libraryJSON is a RESTCONF answer in RFC 7951's JSON encoding, whose one
// member is the resource named by its module.
type libraryJSON struct {
	Library *YANGLibrary  `json:"ietf-yang-library:yang-library"`
	State   *ModulesState `json:"ietf-yang-library:modules-state"`
}

// ParseJSON reads RFC 7951's JSON encoding of the yang-library or
// modules-state resource. An answer holding neither member is an error.
func ParseJSON(raw []byte) (Library, error) {
	var doc libraryJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Library{}, fmt.Errorf("reading the YANG library: %w", err)
	}
	switch {
	case doc.Library != nil:
		return doc.Library.Library(), nil
	case doc.State != nil:
		return doc.State.Library(), nil
	}
	return Library{}, errors.New("reading the YANG library: the answer holds neither ietf-yang-library:yang-library nor ietf-yang-library:modules-state")
}

// Kept is a library's read after the refusal rules: what a Discovery takes
// from it.
type Kept struct {
	// Source is the library's.
	Source string
	// Modules are the entries KeepModules kept.
	Modules []Module
	// Datastores are the library's datastore names that are identifiers,
	// sorted, once each.
	Datastores []string
	// ContentID is the library's, or empty when it is not a short printable
	// token (ValidID).
	ContentID string
	// Dropped and DroppedSamples count what was refused, beside what the
	// Discovery Keep was given had already counted.
	Dropped        int
	DroppedSamples []string
	// ModulesDropped counts the library's own refused module entries, the
	// only refusals that make its list Partial.
	ModulesDropped int
}

// Keep holds lib to the refusal rules. base carries refusals already counted
// (a NETCONF hello's protocol entries), which the counts Kept returns
// include, since the library replaces the hello's module list and what it
// drops is counted afresh beside them. A library that keeps no module is
// ErrEmptyLibrary.
func Keep(lib Library, base Discovery) (Kept, error) {
	counted := Discovery{Dropped: base.Dropped, DroppedSamples: base.DroppedSamples}
	kept := KeepModules(lib.Modules, &counted)
	if len(kept) == 0 {
		return Kept{}, ErrEmptyLibrary
	}
	return Kept{
		Source:         lib.Source,
		Modules:        kept,
		Datastores:     Datastores(lib.Datastores),
		ContentID:      ValidID(lib.ContentID),
		Dropped:        counted.Dropped,
		DroppedSamples: counted.DroppedSamples,
		ModulesDropped: counted.ModulesDropped,
	}, nil
}

// WithLibrary is d completed from a kept library read: its modules, source
// and counted refusals, and Complete unless one of the library's module
// entries was dropped; a refused protocol capability, counted in Dropped
// alone, leaves the list Complete. The
// datastores and the content-id are left as they are, for the caller, which
// knows what else names them (a NETCONF hello's capabilities).
func (d Discovery) WithLibrary(k Kept) Discovery {
	d.Modules, d.Source, d.Dropped, d.DroppedSamples, d.ModulesDropped = k.Modules, k.Source, k.Dropped, k.DroppedSamples, k.ModulesDropped
	d.Completeness = Complete
	if d.ModulesDropped > 0 {
		d.Completeness = Partial
	}
	return d
}

// Datastores is names with every one that is not a YANG identifier left
// out, sorted, once each; nil when none is left.
func Datastores(names []string) []string {
	return Identifiers(names)
}

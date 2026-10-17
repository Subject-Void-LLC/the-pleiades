// Package sbomcheck reads an SPDX 2.3 JSON SBOM, holds it to the NTIA
// minimum elements, and sets the supplier of the thing it describes.
//
// The NTIA's minimum elements (The Minimum Elements For a Software Bill of
// Materials, 2021) are, for each component: a supplier, a name, a version
// and another unique identifier; and for the document: dependency
// relationships, the author of the SBOM data, and a timestamp. A
// generator rarely knows a dependency's supplier, so a missing one is
// reported apart from the rest, for the caller to decide on.
package sbomcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// MaxDocumentBytes bounds an SBOM.
const MaxDocumentBytes = 64 << 20

// noAssertion is SPDX's "nobody knows".
const noAssertion = "NOASSERTION"

// maxNamed bounds a name or value from the document as a message quotes
// it.
const maxNamed = 256

// named is text from the document as a message quotes it: bounded, and
// with anything a terminal would act on made visible.
func named(s string) string { return remoteexec.Excerpt(s, maxNamed) }

// Document is a parsed SPDX 2.3 JSON document: the fields this reads, and
// the whole document, kept so an edit writes back everything it held.
type Document struct {
	raw           map[string]any
	SPDXVersion   string
	Name          string
	Namespace     string
	Created       string
	Creators      []string
	Packages      []Package
	Relationships []Relationship
}

// Package is one SPDX package: a component.
type Package struct {
	SPDXID      string
	Name        string
	Version     string
	Supplier    string
	Identifiers []string
}

// Relationship is one SPDX relationship.
type Relationship struct {
	From, Type, To string
}

// Parse reads an SPDX 2.3 JSON document, refusing anything else.
func Parse(data []byte) (*Document, error) {
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("the SBOM is over %d bytes", MaxDocumentBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("the SBOM is not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the SBOM holds more than one JSON document")
	}
	d := &Document{raw: raw}
	d.SPDXVersion, _ = raw["spdxVersion"].(string)
	if d.SPDXVersion != "SPDX-2.3" {
		return nil, fmt.Errorf("the SBOM is \"%s\", not SPDX-2.3", named(d.SPDXVersion))
	}
	d.Name, _ = raw["name"].(string)
	d.Namespace, _ = raw["documentNamespace"].(string)
	if info, ok := raw["creationInfo"].(map[string]any); ok {
		d.Created, _ = info["created"].(string)
		d.Creators = stringList(info["creators"])
	}
	pkgs, _ := raw["packages"].([]any)
	for i, item := range pkgs {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("package %d is not an object", i)
		}
		p := Package{}
		p.SPDXID, _ = m["SPDXID"].(string)
		p.Name, _ = m["name"].(string)
		p.Version, _ = m["versionInfo"].(string)
		p.Supplier, _ = m["supplier"].(string)
		refs, _ := m["externalRefs"].([]any)
		for _, r := range refs {
			ref, _ := r.(map[string]any)
			typ, _ := ref["referenceType"].(string)
			loc, _ := ref["referenceLocator"].(string)
			if (typ == "purl" || strings.HasPrefix(typ, "cpe")) && loc != "" {
				p.Identifiers = append(p.Identifiers, loc)
			}
		}
		if p.SPDXID == "" {
			return nil, fmt.Errorf("package %d (\"%s\") has no SPDXID", i, named(p.Name))
		}
		d.Packages = append(d.Packages, p)
	}
	rels, _ := raw["relationships"].([]any)
	for _, item := range rels {
		m, _ := item.(map[string]any)
		r := Relationship{}
		r.From, _ = m["spdxElementId"].(string)
		r.Type, _ = m["relationshipType"].(string)
		r.To, _ = m["relatedSpdxElement"].(string)
		d.Relationships = append(d.Relationships, r)
	}
	return d, nil
}

// stringList reads a JSON list of strings.
func stringList(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Subjects are the packages the document describes.
func (d *Document) Subjects() []string {
	var out []string
	for _, r := range d.Relationships {
		if r.From == "SPDXRef-DOCUMENT" && r.Type == "DESCRIBES" {
			out = append(out, r.To)
		}
	}
	return out
}

// Report is how a document meets the NTIA minimum elements.
type Report struct {
	// Components is how many packages the document holds.
	Components int
	// Dependencies is how many of them are not a described subject: none
	// means the generator found nothing in what it described.
	Dependencies int
	// Problems are the failures that stand regardless: the document's own
	// elements, and components without a name, version or identifier.
	Problems []string
	// MissingSupplier names every dependency with no supplier, which a
	// generator rarely knows; the caller decides whether it fails.
	MissingSupplier []string
}

// listed bounds how many names one problem lists.
const listed = 10

// Validate holds d to the NTIA minimum elements. The described subject
// needs every element, its identifier being its name and version within
// the document's unique namespace (a built directory has no purl); every
// other component needs a name, a version and a purl or CPE, and a
// supplier, which is reported apart.
func (d *Document) Validate() Report {
	r := Report{Components: len(d.Packages)}
	if len(d.Creators) == 0 {
		r.Problems = append(r.Problems, "the document names no author (creationInfo.creators)")
	}
	if _, err := time.Parse(time.RFC3339, d.Created); err != nil {
		r.Problems = append(r.Problems, fmt.Sprintf("the document has no timestamp (creationInfo.created is \"%s\")", named(d.Created)))
	}
	if !strings.Contains(d.Namespace, "://") {
		r.Problems = append(r.Problems, "the document has no namespace URI, so nothing in it is uniquely identified")
	}
	subjects := map[string]bool{}
	for _, s := range d.Subjects() {
		subjects[s] = true
	}
	if len(subjects) == 0 {
		r.Problems = append(r.Problems, "the document describes nothing (no DESCRIBES relationship)")
	}
	// A subject must be a package the document holds: one it does not
	// hold has no name, version or supplier to check.
	if missing := d.unheldSubjects(); len(missing) > 0 {
		r.Problems = append(r.Problems, fmt.Sprintf("%d DESCRIBES relationship(s) name no package in the document: %s", len(missing), first(missing)))
	}
	related := map[string]bool{}
	for _, rel := range d.Relationships {
		if rel.From != "SPDXRef-DOCUMENT" {
			related[rel.From] = true
			related[rel.To] = true
		}
	}
	var noName, noVersion, noID, unrelated []string
	for _, p := range d.Packages {
		name := p.Name
		if name == "" {
			name = p.SPDXID
			noName = append(noName, p.SPDXID)
		}
		if p.Version == "" || p.Version == noAssertion {
			noVersion = append(noVersion, name)
		}
		if subjects[p.SPDXID] {
			if p.Supplier == "" || p.Supplier == noAssertion {
				r.Problems = append(r.Problems, fmt.Sprintf("the subject %s has no supplier", named(name)))
			}
			continue
		}
		r.Dependencies++
		if len(p.Identifiers) == 0 {
			noID = append(noID, name)
		}
		if p.Supplier == "" || p.Supplier == noAssertion {
			r.MissingSupplier = append(r.MissingSupplier, name)
		}
		if !related[p.SPDXID] && len(d.Packages) > 1 {
			unrelated = append(unrelated, name)
		}
	}
	for _, gap := range []struct {
		what  string
		names []string
	}{{"no name", noName}, {"no version", noVersion}, {"no purl or CPE", noID}, {"no dependency relationship", unrelated}} {
		if len(gap.names) > 0 {
			r.Problems = append(r.Problems, fmt.Sprintf("%d component(s) with %s: %s", len(gap.names), gap.what, first(gap.names)))
		}
	}
	return r
}

// unheldSubjects is every described element the document holds no
// package for.
func (d *Document) unheldSubjects() []string {
	held := map[string]bool{}
	for _, p := range d.Packages {
		held[p.SPDXID] = true
	}
	var out []string
	for _, s := range d.Subjects() {
		if !held[s] {
			out = append(out, s)
		}
	}
	return out
}

// first is up to listed names, sorted, each bounded (named), and how
// many more.
func first(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	more := ""
	if len(sorted) > listed {
		more = fmt.Sprintf(" and %d more", len(sorted)-listed)
		sorted = sorted[:listed]
	}
	for i, n := range sorted {
		sorted[i] = named(n)
	}
	return strings.Join(sorted, ", ") + more
}

// SetSubjectSupplier records supplier ("Organization: Example Co" or
// "Person: ...") on the described subjects only, never on a dependency,
// whose supplier the operator does not speak for, and adds tool to the
// document's creators, since the document is no longer the generator's
// alone. It returns the document as JSON.
func (d *Document) SetSubjectSupplier(supplier, tool string) ([]byte, error) {
	if !strings.HasPrefix(supplier, "Organization: ") && !strings.HasPrefix(supplier, "Person: ") {
		return nil, fmt.Errorf("supplier %q is not \"Organization: name\" or \"Person: name\"", supplier)
	}
	subjects := map[string]bool{}
	for _, s := range d.Subjects() {
		subjects[s] = true
	}
	if len(subjects) == 0 {
		return nil, errors.New("the document describes nothing, so no subject can be given a supplier")
	}
	if missing := d.unheldSubjects(); len(missing) > 0 {
		return nil, fmt.Errorf("the document describes %s, which is no package in it, so no supplier can be set on it", first(missing))
	}
	pkgs, _ := d.raw["packages"].([]any)
	for i, item := range pkgs {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := m["SPDXID"].(string); subjects[id] {
			m["supplier"] = supplier
			d.Packages[i].Supplier = supplier
		}
	}
	if info, ok := d.raw["creationInfo"].(map[string]any); ok {
		creators, _ := info["creators"].([]any)
		info["creators"] = append(creators, "Tool: "+tool)
		d.Creators = append(d.Creators, "Tool: "+tool)
	}
	return json.MarshalIndent(d.raw, "", " ")
}

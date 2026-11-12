// Package capability: the digest that binds a grant to what a data
// capability means, and the revision an author bumps to change it.
package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
)

// A grant of a data capability says a device was found to fit one
// definition of it. If the definition later means something else (a new
// required property, a new compiled requirement, or an author's own word
// that it changed), the old grant is evidence about a different question
// and grants nothing until the device is probed or vouched for again.
//
// The binding is to the definition, never to the program that defines it.
// A program's SHA-256 differs per operating system and architecture, so a
// grant bound to it and probed by a macOS command line would never hold on
// a Linux Runner, and an operator's assertion was made by no build at all.
// The probing build's digest is still recorded with a probed grant, as
// provenance an admin can revoke by (inventory.Grant.Build).

// DefinitionDigestPrefix starts every definition digest: the hash it was
// made with, as a program digest and a binding digest start.
const DefinitionDigestPrefix = "sha256:"

// MaxDataRevision bounds a definition's Revision. A revision counts the
// times an author has said a definition changed meaning, so a bound this
// large is never met by an honest program and keeps the number exact in
// every JSON reader.
const MaxDataRevision = 1_000_000

// definitionDomain separates this digest from every other SHA-256 this
// module makes, so no other encoding can be read as a definition's.
const definitionDomain = "pleiades capability definition v1\n"

// definitionDigestPattern is the shape DefinitionDigest returns.
var definitionDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// definitionForm is what a definition digest covers, in a fixed order.
// Prose (Doc and each property's Description) is left out, so a corrected
// sentence never withdraws a grant. Probe is left out too: the probe is
// how a grant is found, not what it means, and a fixed probe that would no
// longer grant is the author's cue to bump Revision.
type definitionForm struct {
	Name       Name           `json:"name"`
	Provider   string         `json:"provider"`
	Revision   int            `json:"revision"`
	Requires   []Name         `json:"requires"`
	Properties []propertyForm `json:"properties"`
}

// propertyForm is what a definition digest covers of one property.
type propertyForm struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// DefinitionDigest returns the digest a grant of def carries
// ("sha256:<hex>"): a SHA-256 over def's name, provider, revision,
// requirements and properties (each one's name, type and whether it is
// required), with the requirements and properties sorted and a requirement
// named twice counted once, so the order a program lists them in never
// matters. Nothing in it depends on the platform the program was built
// for or the process computing it: the same definition digests the same
// on a macOS command line, a Linux Runner and a Controller.
func DefinitionDigest(def DataDefinition) string {
	form := definitionForm{
		Name:       def.Name,
		Provider:   def.Provider,
		Revision:   def.Revision,
		Requires:   []Name{},
		Properties: []propertyForm{},
	}
	seen := map[Name]bool{}
	for _, r := range def.Requires {
		if !seen[r] {
			seen[r] = true
			form.Requires = append(form.Requires, r)
		}
	}
	sort.Slice(form.Requires, func(i, j int) bool { return form.Requires[i] < form.Requires[j] })
	for _, p := range def.Properties {
		form.Properties = append(form.Properties, propertyForm{Name: p.Name, Type: p.Type, Required: p.Required})
	}
	// By name, then by the rest, so two properties of one name (which
	// ValidateData refuses) still sort the same way every time.
	sort.Slice(form.Properties, func(i, j int) bool {
		a, b := form.Properties[i], form.Properties[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return !a.Required && b.Required
	})
	// A struct of strings, ints, bools and slices of them always encodes.
	encoded, err := json.Marshal(form)
	if err != nil {
		panic("capability: a definition's digest form did not encode: " + err.Error())
	}
	sum := sha256.Sum256(append([]byte(definitionDomain), encoded...))
	return DefinitionDigestPrefix + hex.EncodeToString(sum[:])
}

// IsDefinitionDigest reports whether s has the shape DefinitionDigest
// returns. It is all a process holding no definition (a Controller) can
// check of one a client supplies.
func IsDefinitionDigest(s string) bool {
	return definitionDigestPattern.MatchString(s)
}

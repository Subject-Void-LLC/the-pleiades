// This file is the bodies PATCH /credential-types/{id} and PATCH
// /credentials/{id} accept, and how each is folded over the stored record.
//
// Each is a partial update, as the template, access and project PATCHes are
// (templates_patch.go, access_patch.go, projects_patch.go): a field the body
// leaves out keeps its stored value and a field it names is applied. Both
// routes used to decode their create body, so a field the body left out was
// written back empty (FAILURE_PATTERNS 1075). A credential PATCH that did not
// name external erased every external secret reference the credential held,
// which saves cleanly and fails the next run that needs the secret, and one
// that did not name the name answered 500. A type PATCH that did not name
// the name or the kind was refused for the one it blanked, and one naming
// both erased the input schema and the injector document.
//
// What naming a field does depends on its shape, and the edge cases decided
// each:
//
//   - A type's inputs and injectors are documents, and naming one replaces it
//     whole, as PUT .../inputs and PUT .../injectors do. Merging fields by id
//     would leave no way to remove or reorder one, and the type reads back in
//     full, so a client edits what GET showed. Inside a document, a null
//     decodes as a create decodes it.
//   - A credential's inputs merges over the stored values, as it always did:
//     an input left out keeps its value, the marker "$encrypted$" keeps a
//     stored secret, and "" clears an input. It cannot replace them whole,
//     because a secret reads back only as the marker, so a client can never
//     send back a value it was not shown.
//   - A credential's external, when named, is the whole new set of
//     references, as PUT .../input-sources replaces the row form's whole
//     set: an input left out of it no longer reads from an external source,
//     and {} removes every reference. A reference reads back in full (it is a
//     pointer to a secret, never one), so a client can edit what GET showed,
//     and this is the one way a reference is removed: a null is refused.
//
// The rules are the shared ones in patch.go. A null on any field is refused,
// naming it, and so is a null value inside a credential's inputs or external,
// which used to decode as "": an optional secret given a null was silently
// cleared with a 200. An unknown key, and a body that is not an object, are
// malformed. The fields no update can change are refused when they differ
// from the stored ones and accepted when they equal them: a type's namespace
// and organization, and a credential's credential_type and organization.
// Each used to be decoded and ignored, answering 200 for a change that was
// never made (FAILURE_PATTERNS 1055).
//
// The handler hands the store a fold, which the store runs on the record it
// reads under the type's lock and then writes (UpdateTypeWith,
// UpdateCredentialWith), so a type edit landing between a read and a write
// cannot be undone by the write, and every rule still runs on what would be
// saved: Validate for a type (its inputs, its injectors compiled against
// them, its kind), CheckValues for a credential (every required input still
// supplied, every value within its choices, every input and reference one
// the type declares), the text rules (a blank name, a NUL), the managed
// refusal, and a taken name. The web UI's forms fold their changes the same
// way.
//
// Two PATCHes of one type's records are serialized by the type's lock, and
// each folds over what the other wrote, so neither undoes a field the other
// set that it does not name itself. There is still no version check: of two
// PATCHes naming the same field, the second's value wins.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// credentialTypePatchDTO is the body PATCH /credential-types/{id} accepts:
// every field of the create body, each a pointer so an absent key keeps its
// stored value.
type credentialTypePatchDTO struct {
	Name        *string        `json:"name"`
	Description *string        `json:"description"`
	Kind        *credtype.Kind `json:"kind"`

	// Inputs and Injectors, when named, replace the stored document whole.
	Inputs    *credtype.InputSchema `json:"inputs"`
	Injectors *credtype.Injectors   `json:"injectors"`

	// Namespace and Organization are fixed. The namespace is what an AWX
	// import keys on to find this type again, so changing it would make the
	// next import create a duplicate; the organization is the tenant whose
	// credentials may use it. Each is accepted only as the stored value,
	// which is 0 for a managed type's organization.
	Namespace    *string `json:"namespace"`
	Organization *int    `json:"organization"`

	// nulls names every field the body set to JSON null, sorted.
	// Unexported, so decoding never fills it.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *credentialTypePatchDTO) UnmarshalJSON(data []byte) error {
	// fields is the same struct without this method, so decoding into it
	// does not recurse back here.
	type fields credentialTypePatchDTO
	decoded, nulls, err := decodePatch[fields](data, d.noun())
	if err != nil {
		return err
	}
	*d = credentialTypePatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// noun names a credential type in the shared PATCH refusals (patch.go).
//
// A method rather than a package constant, as templateNoun and projectNoun
// are, because gosec's G101 reads a constant whose name holds "cred" and
// whose text it scores as random as a hardcoded secret, and a waiver keyed
// on a line number breaks whenever a line above it moves.
func (credentialTypePatchDTO) noun() string { return "a credential type" }

// nullRefusal is the 400 message for a body that set fields to null.
func (d credentialTypePatchDTO) nullRefusal() string {
	return patchNullRefusal(d.noun(), d.nulls)
}

// fixedFieldRefusal is the 400 message for a body that asks to change the
// namespace or the organization, or "" when it does not.
func (d credentialTypePatchDTO) fixedFieldRefusal(stored credstore.CredentialType) string {
	// remedy is what a refused change to a fixed field says to do.
	const remedy = "create a new credential type instead"
	switch {
	case d.Namespace != nil && *d.Namespace != stored.Namespace:
		return fixedFieldMessage("namespace", d.noun(), remedy)
	case d.Organization != nil && *d.Organization != stored.OrganizationID:
		return fixedFieldMessage("organization", d.noun(), remedy)
	}
	return ""
}

// apply folds the fields the body named over stored. The namespace and the
// managed flag are carried along untouched; the store writes neither.
func (d credentialTypePatchDTO) apply(stored credtype.CredentialType) credtype.CredentialType {
	patchField(&stored.Name, d.Name)
	patchField(&stored.Description, d.Description)
	patchField(&stored.Kind, d.Kind)
	patchField(&stored.Inputs, d.Inputs)
	patchField(&stored.Injectors, d.Injectors)
	return stored
}

// credentialPatchDTO is the body PATCH /credentials/{id} accepts: every field
// of the create body, each a pointer so an absent key keeps its stored value.
type credentialPatchDTO struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`

	// Inputs merges over the stored values (see this file's comment), so
	// leaving it out and sending {} both change nothing.
	Inputs *patchStrings `json:"inputs"`

	// External, when named, replaces the stored references whole.
	External *patchStrings `json:"external"`

	// CredentialType and Organization are fixed. A new type would
	// reinterpret every stored value under another schema (the ent edge is
	// Immutable for that reason), and a new organization would hand the
	// secrets to another tenant's templates. Each is accepted only as the
	// stored value.
	CredentialType *int `json:"credential_type"`
	Organization   *int `json:"organization"`

	// nulls names every field the body set to JSON null, sorted.
	// Unexported, so decoding never fills it.
	nulls []string
}

// UnmarshalJSON decodes the body strictly and records which fields were null.
func (d *credentialPatchDTO) UnmarshalJSON(data []byte) error {
	type fields credentialPatchDTO
	decoded, nulls, err := decodePatch[fields](data, d.noun())
	if err != nil {
		return err
	}
	*d = credentialPatchDTO(decoded)
	d.nulls = nulls
	return nil
}

// patchStrings is a JSON object of strings, keyed by input id, that records
// whether any of its values was null.
//
// A plain map[string]string decodes a null value as "", which for inputs is
// the value that clears a stored input, so an optional secret sent as null
// was cleared with a 200. Recording the null lets the handler refuse it.
type patchStrings struct {
	// values is the object's string values, by key.
	values map[string]string
	// hasNull reports whether any value was null; such a key is left out of
	// values.
	hasNull bool
}

// UnmarshalJSON decodes one object of strings. A value that is neither a
// string nor null makes the body malformed, as it did for the plain map. A
// null for the whole object never reaches here: the field is a pointer, which
// encoding/json sets to nil, and decodePatch records the null by name.
func (m *patchStrings) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if m.values == nil {
		m.values = make(map[string]string, len(raw))
	}
	for key, value := range raw {
		if string(bytes.TrimSpace(value)) == "null" {
			m.hasNull = true
			continue
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return err
		}
		m.values[key] = text
	}
	return nil
}

// holdsNull reports whether m was named and held a null value.
func (m *patchStrings) holdsNull() bool { return m != nil && m.hasNull }

// noun names a credential in the shared PATCH refusals (patch.go). It is a
// method for the reason credentialTypePatchDTO.noun gives.
func (credentialPatchDTO) noun() string { return "a credential" }

// nullRefusal is the 400 message for a body that set a field, or a value
// inside inputs or external, to null, or "" when it set none. It never names
// the key inside a map, which is the caller's own bytes.
func (d credentialPatchDTO) nullRefusal() string {
	switch {
	case len(d.nulls) > 0:
		return patchNullRefusal(d.noun(), d.nulls)
	case d.Inputs.holdsNull():
		return `inputs may not hold a null value: leave an input out to keep its stored value, or send "" to clear it`
	case d.External.holdsNull():
		return "external may not hold a null value: send the whole set of references without an input to remove " +
			"its reference, or {} to remove them all"
	}
	return ""
}

// fixedFieldRefusal is the 400 message for a body that asks to change the
// credential's type or organization, or "" when it does not.
func (d credentialPatchDTO) fixedFieldRefusal(stored credstore.Credential) string {
	// remedy is what a refused change to a fixed field says to do.
	const remedy = "create a new credential instead"
	switch {
	case d.CredentialType != nil && *d.CredentialType != stored.TypeID:
		return fixedFieldMessage("credential_type", d.noun(), remedy)
	case d.Organization != nil && *d.Organization != stored.OrganizationID:
		return fixedFieldMessage("organization", d.noun(), remedy)
	}
	return ""
}

// apply folds the fields the body named over stored, the redacted read the
// store took under the type's lock. Inputs carries only the values the body
// supplied, never stored.Inputs: those are the redacted projection, and the
// store's merge already keeps every input the body did not name.
func (d credentialPatchDTO) apply(stored credstore.Credential) credstore.Credential {
	patchField(&stored.Name, d.Name)
	patchField(&stored.Description, d.Description)
	stored.Inputs = nil
	if d.Inputs != nil {
		stored.Inputs = d.Inputs.values
	}
	if d.External != nil {
		stored.External = d.External.values
	}
	return stored
}

// failPatch answers a refusal a PATCH's fold made with 400, and any other
// error as fail does.
func (h *CredentialHandler) failPatch(w http.ResponseWriter, r *http.Request, err error) {
	var refusal patchRefusal
	if errors.As(err, &refusal) {
		RespondError(w, r, http.StatusBadRequest, string(refusal))
		return
	}
	h.fail(w, r, err)
}

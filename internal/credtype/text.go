// This file holds the rule that a credential type, and a credential, carry
// only text the database can store.
//
// PostgreSQL refuses a NUL character in a character varying column and in a
// jsonb document, and a credential type keeps its name and description in
// the first and its input schema and injector document in the second, as a
// credential keeps its name and description in the first and its external
// references in the second. SQLite stores the NUL, so a write that passed on
// one dialect failed on the other with a 500 (the dialect gap of
// FAILURE_PATTERNS 371, 1079 and 1092). The rule is checked where every
// writer meets it: Validate for a type, and the credential store for a
// credential, so the web UI's forms are held to it as the API is.
package credtype

import (
	"strings"
)

// TextError is the refusal of text the database cannot store: a NUL
// character in a field, or a blank credential name. Its text names the
// field and repeats nothing the caller sent, so a handler or a form may show
// it as it is.
type TextError struct {
	// Field is the field's wire name: "name", "description", "external",
	// "inputs" or "injectors". A NUL inside a document is reported by the
	// document, never by a key inside it, which is the caller's own bytes.
	Field string
	// Blank is true for a required name left empty, false for a NUL.
	Blank bool
	// Kind is ErrInvalidType for a credential type and ErrInvalidCredential
	// for a credential, which is what errors.Is matches.
	Kind error
}

// Error says which field is refused and why.
func (e *TextError) Error() string {
	if e.Blank {
		return e.Field + " is required"
	}
	return e.Field + " may not contain a NUL character: the database cannot store one"
}

// Unwrap returns the sentinel the refusal belongs to.
func (e *TextError) Unwrap() error { return e.Kind }

// HasNUL reports whether s holds a NUL character.
func HasNUL(s string) bool { return strings.ContainsRune(s, 0) }

// nulRefusal returns a TextError for the type's first field, in the order
// name, description, inputs, injectors, that holds a NUL anywhere, or nil.
// The kind and the namespace need no check of their own: each is held to a
// closed set or a pattern a NUL cannot match.
func (ct CredentialType) nulRefusal() error {
	switch {
	case HasNUL(ct.Name):
		return &TextError{Field: "name", Kind: ErrInvalidType}
	case HasNUL(ct.Description):
		return &TextError{Field: "description", Kind: ErrInvalidType}
	case ct.Inputs.holdsNUL():
		return &TextError{Field: "inputs", Kind: ErrInvalidType}
	case ct.Injectors.holdsNUL():
		return &TextError{Field: "injectors", Kind: ErrInvalidType}
	}
	return nil
}

// holdsNUL reports whether any text in the schema holds a NUL.
func (s InputSchema) holdsNUL() bool {
	for _, f := range s.Fields {
		if anyNUL(f.ID, f.Label, string(f.Type), string(f.Format), f.Default, f.Help) || anyNUL(f.Choices...) {
			return true
		}
	}
	return anyNUL(s.Required...)
}

// holdsNUL reports whether any name or template in the document holds a
// NUL, extra variables walked to every leaf.
func (inj Injectors) holdsNUL() bool {
	for name, template := range inj.Env {
		if anyNUL(name, template) {
			return true
		}
	}
	for name, template := range inj.File {
		if anyNUL(name, template) {
			return true
		}
	}
	return anyNUL(inj.OmitEmpty...) || valueHoldsNUL(inj.ExtraVars)
}

// valueHoldsNUL walks a decoded extra-variables value, any of whose keys or
// string leaves may hold a NUL. A list is not walked: Validate refuses an
// extra variable that is one.
func valueHoldsNUL(v any) bool {
	switch value := v.(type) {
	case string:
		return HasNUL(value)
	case map[string]any:
		for key, nested := range value {
			if HasNUL(key) || valueHoldsNUL(nested) {
				return true
			}
		}
	}
	return false
}

// anyNUL reports whether any of values holds a NUL.
func anyNUL(values ...string) bool {
	for _, v := range values {
		if HasNUL(v) {
			return true
		}
	}
	return false
}

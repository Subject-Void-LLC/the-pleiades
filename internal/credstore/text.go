// This file holds the text rules a credential is held to before the store
// writes it, from the API and from the web UI's form alike.
//
// Neither ent schema bounds a name's length (internal/ent/schema/credential.go
// declares name NotEmpty and description Optional, with no MaxLen), so there
// is no length rule. What the store could not save was a blank name, which
// reached ent's NotEmpty validator with an error nothing classified, so the
// API answered 500, and a NUL character, which SQLite stores and PostgreSQL
// refuses in a character varying column and in the jsonb that holds the
// external references (credtype/text.go), and in the jsonb that holds an
// input source binding's metadata. A credential type's text is held to the
// same rule by credtype.CredentialType.Validate.
package credstore

import "github.com/Subject-Void-LLC/the-pleiades/internal/credtype"

// checkCredentialText refuses a blank name and a NUL in the name, the
// description or an external reference, with a credtype.TextError naming
// the field. A NUL in a reference is reported as external rather than by
// the input it belongs to, since that key is the caller's own bytes until
// CheckValues has held it to the type.
func checkCredentialText(name, description string, external map[string]string) error {
	if name == "" {
		return &credtype.TextError{Field: "name", Blank: true, Kind: credtype.ErrInvalidCredential}
	}
	if credtype.HasNUL(name) {
		return &credtype.TextError{Field: "name", Kind: credtype.ErrInvalidCredential}
	}
	if credtype.HasNUL(description) {
		return &credtype.TextError{Field: "description", Kind: credtype.ErrInvalidCredential}
	}
	for _, reference := range external {
		if credtype.HasNUL(reference) {
			return &credtype.TextError{Field: "external", Kind: credtype.ErrInvalidCredential}
		}
	}
	return nil
}

// checkMetadataText refuses a NUL in a key or a value of an input source
// binding's metadata, which is kept as jsonb, with a credtype.TextError on
// metadata. It names the document, never a key inside it, which is the
// caller's own bytes.
func checkMetadataText(metadata map[string]string) error {
	for key, value := range metadata {
		if credtype.HasNUL(key) || credtype.HasNUL(value) {
			return &credtype.TextError{Field: "metadata", Kind: credtype.ErrInvalidCredential}
		}
	}
	return nil
}

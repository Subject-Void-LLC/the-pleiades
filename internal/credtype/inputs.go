package credtype

import (
	"fmt"
	"regexp"
	"sort"
)

// The input schema: what a credential of a given type holds.
//
// # Why this is not launch.Survey
//
// internal/launch already has a hand-rolled typed input schema with an
// ordered field list, a secret flag, a required set, choices and defaults.
// The overlap is real and merging them was considered and rejected, for a
// reason that is worth stating here rather than leaving to be rediscovered.
//
// launch.Survey's own doc comment states the governing principle: it uses
// AWX's type names deliberately, so a survey imported from an AWX job
// template means the same thing here that it meant there. Obeying that
// principle forces two types, because AWX has two vocabularies:
//
//   - A survey question has seven types (text, textarea, password, integer,
//     float, multiplechoice, multiselect) and encodes secrecy IN the type,
//     as "password".
//   - A credential input has two types (string, boolean) and encodes
//     secrecy in an ORTHOGONAL boolean, beside an orthogonal format.
//
// Merging them produces either a survey question that can be boolean and a
// credential input that can be multiselect, neither of which AWX nor this
// platform can hold, so an import would produce values with nowhere to go;
// or a shared supertype plus two constraint sets, which is more code than
// the two flat types and hides which constraint belongs to which entity.
//
// What IS shared, and must be, is the secret-decision seam.
// launch.Survey.SecretVariables and InputSchema.SecretFields are each the
// single place their entity decides what is secret, and both feed
// redact.Literals. Two places deciding that would eventually disagree, and
// the disagreement would be silent in the dangerous direction.

// InputType is the type of one credential input value.
//
// Two values, matching AWX. Everything a credential holds is stored and
// transmitted as a string, including a boolean, because that is what AWX
// does and because an injector template renders text either way.
type InputType string

const (
	// InputString is the default and covers almost everything.
	InputString InputType = "string"

	// InputBoolean is a flag, rendered as "true" or "false".
	InputBoolean InputType = "boolean"
)

// InputFormat is an optional hint about a string input's content.
//
// AWX uses it to pick a form control and to validate on save. Here it is
// carried faithfully and acted on only where this platform has something
// real to do with it, which today is the vault identifier.
type InputFormat string

const (
	// FormatNone is the absence of a format.
	FormatNone InputFormat = ""

	// FormatSSHPrivateKey marks a PEM private key body, which a form
	// renders as a multi-line control.
	FormatSSHPrivateKey InputFormat = "ssh_private_key"

	// FormatURL marks a URL.
	FormatURL InputFormat = "url"

	// FormatVaultID marks the vault identifier the binding rule reads.
	FormatVaultID InputFormat = "vault_id"
)

// inputIDPattern constrains an input identifier.
//
// The constraint is tighter than it looks like it needs to be, and the
// reason is that an id is used in two places at once. It is a variable name
// in an injector template, where the renderer's own grammar already
// requires this shape, and it is commonly the basis of an environment
// variable name. Allowing a hyphen or a leading digit would produce ids
// that parse in one place and not the other.
var inputIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// InputField is one field a credential of this type holds.
type InputField struct {
	// ID is the identifier an injector template references.
	ID string `json:"id"`

	// Label is what a form shows.
	Label string `json:"label"`

	// Type is string or boolean. An empty value means string, which is
	// how AWX writes it: the corpus fixture omits the type on neither
	// field, but many managed types do.
	Type InputType `json:"type,omitempty"`

	// Secret decides encryption at rest and redaction on the way out.
	// It is the single most consequential bit in this struct.
	Secret bool `json:"secret,omitempty"`

	// Multiline marks a value a form should render as a text area.
	Multiline bool `json:"multiline,omitempty"`

	// Format is an optional content hint.
	Format InputFormat `json:"format,omitempty"`

	// Choices bounds the accepted values.
	Choices []string `json:"choices,omitempty"`

	// Default is the value used when a credential omits this field. A
	// secret field may not carry one; see InputSchema.Validate.
	Default string `json:"default,omitempty"`

	// Help is the explanatory text a form shows beneath the control. The
	// tag is AWX's own name for it.
	Help string `json:"help_text,omitempty"`

	// AskAtRuntime marks an input prompted at launch rather than stored.
	// A value supplied for it is never persisted into a saved launch
	// configuration; PLAN.md Section 29.3's never-persist rule.
	AskAtRuntime bool `json:"ask_at_runtime,omitempty"`
}

// InputSchema is a credential type's whole input schema.
type InputSchema struct {
	// Fields are the inputs, in the order a form should show them.
	Fields []InputField `json:"fields,omitempty"`

	// Required names the fields a credential must supply.
	Required []string `json:"required,omitempty"`
}

// Field returns the named field.
func (s InputSchema) Field(id string) (InputField, bool) {
	for _, f := range s.Fields {
		if f.ID == id {
			return f, true
		}
	}
	return InputField{}, false
}

// IDs returns every declared input id, in declaration order.
func (s InputSchema) IDs() []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		out = append(out, f.ID)
	}
	return out
}

// SecretFields returns the ids of every field marked secret, sorted.
//
// This is the seam. It is the one place this entity decides what is
// secret, and it feeds encryption at rest, redaction in an API response,
// and registration with the masking ruleset. launch.Survey.SecretVariables
// is the identical seam for survey answers; see this file's own comment for
// why the two types are parallel rather than merged.
func (s InputSchema) SecretFields() []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		if f.Secret {
			out = append(out, f.ID)
		}
	}
	sort.Strings(out)
	return out
}

// AskAtRuntimeFields returns the ids of every field prompted at launch,
// sorted. A type declaring any of these makes its credentials
// non-relaunchable, since the answer was never stored.
func (s InputSchema) AskAtRuntimeFields() []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		if f.AskAtRuntime {
			out = append(out, f.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Validate reports whether the schema is well formed.
func (s InputSchema) Validate() error {
	seen := make(map[string]struct{}, len(s.Fields))

	for i, f := range s.Fields {
		if !inputIDPattern.MatchString(f.ID) {
			return fmt.Errorf(
				"%w: input %d has id %q, which must be lowercase letters, digits and underscores, starting with a letter",
				ErrInvalidType, i, f.ID)
		}
		if _, dup := seen[f.ID]; dup {
			return fmt.Errorf("%w: two inputs are both named %q", ErrInvalidType, f.ID)
		}
		seen[f.ID] = struct{}{}

		if f.Label == "" {
			return fmt.Errorf("%w: input %q has no label", ErrInvalidType, f.ID)
		}

		switch f.Type {
		case "", InputString, InputBoolean:
		default:
			return fmt.Errorf("%w: input %q has type %q, which is not %q or %q",
				ErrInvalidType, f.ID, f.Type, InputString, InputBoolean)
		}

		// A default on a secret field is a credential stored in the type
		// record, readable by anybody who may edit the type and copied
		// into every credential created from it. launch.Survey.Validate
		// refuses the identical thing for a password question, and the two
		// refusals exist for the same reason.
		if f.Secret && f.Default != "" {
			return fmt.Errorf(
				"%w: input %q is secret and carries a default, which would store a credential in the type itself",
				ErrInvalidType, f.ID)
		}

		if err := validateChoices(f); err != nil {
			return err
		}
	}

	for _, id := range s.Required {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("%w: required names %q, which is not a declared input", ErrInvalidType, id)
		}
	}

	return nil
}

// validateChoices checks a field's bounded value set.
func validateChoices(f InputField) error {
	if len(f.Choices) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(f.Choices))
	for _, c := range f.Choices {
		if c == "" {
			return fmt.Errorf("%w: input %q has an empty choice, which nobody can pick", ErrInvalidType, f.ID)
		}
		if _, dup := seen[c]; dup {
			return fmt.Errorf("%w: input %q lists the choice %q twice", ErrInvalidType, f.ID, c)
		}
		seen[c] = struct{}{}
	}

	if f.Default != "" {
		if _, ok := seen[f.Default]; !ok {
			return fmt.Errorf(
				"%w: input %q defaults to %q, which is not among its choices",
				ErrInvalidType, f.ID, f.Default)
		}
	}
	return nil
}

// CheckValues reports whether values satisfy this schema.
//
// It validates what a credential holds rather than what the type declares:
// every required field present and non-empty, no value for an input the
// type does not declare, and every bounded field inside its own choices.
//
// No error message here ever includes a value. Every one of them can reach
// an API response and a log line, and half of these values are secrets.
func (s InputSchema) CheckValues(values map[string]string) error {
	for id := range values {
		if _, ok := s.Field(id); !ok {
			return fmt.Errorf("%w: %q is not an input this credential type declares", ErrInvalidCredential, id)
		}
	}

	for _, id := range s.Required {
		f, ok := s.Field(id)
		if !ok {
			// Unreachable for a validated schema; a defensive branch here
			// would be dead code, so this reports rather than assumes.
			return fmt.Errorf("%w: required input %q is not declared", ErrInvalidType, id)
		}
		if values[id] == "" && f.Default == "" && !f.AskAtRuntime {
			return fmt.Errorf("%w: input %q is required and was not supplied", ErrInvalidCredential, id)
		}
	}

	for id, v := range values {
		f, _ := s.Field(id)
		if len(f.Choices) == 0 || v == "" {
			continue
		}
		var ok bool
		for _, c := range f.Choices {
			if c == v {
				ok = true
				break
			}
		}
		if !ok {
			// Names the field and the permitted set, never the value that
			// failed: a rejected value is still a value somebody typed
			// into a secret field.
			return fmt.Errorf("%w: input %q must be one of %v", ErrInvalidCredential, id, f.Choices)
		}
	}

	return nil
}

// Defaults returns the declared defaults, as a fresh map. It is what fills
// in the values a credential did not supply, immediately before injection.
func (s InputSchema) Defaults() map[string]string {
	out := make(map[string]string)
	for _, f := range s.Fields {
		if f.Default != "" {
			out[f.ID] = f.Default
		}
	}
	return out
}

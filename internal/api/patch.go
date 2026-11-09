// This file holds what every partial-update (PATCH) body in this package
// shares: the strict decode a custom unmarshaler has to repeat, the record of
// which fields a body set to JSON null, and the wording of the two refusals a
// PATCH makes before it reaches a store.
//
// A PATCH body type here follows one pattern, first built for templates
// (templatePatchDTO) and then for the access resources (organizationPatchDTO,
// teamPatchDTO, contactPatchDTO, bindingPatchDTO). Every field is a pointer, so an absent key can be told from
// a zero value. The type's UnmarshalJSON decodes through decodePatch, which
// refuses an unknown key and a body that is not an object, and records each
// field the body set to null so the handler can refuse it by name, and refuses
// a body that names one field twice (patchRepeatedFieldError). A field no
// update may change is compared against the stored record and refused only
// when it differs, so an echo of the stored value still works.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// patchNotAnObject is the error for a PATCH body, or an object within one,
// that is not a JSON object: a bare null would otherwise decode as an empty
// patch. noun is the resource with its article, such as "a template".
//
// The text never reaches the caller (decodeJSON reports only that the body
// was malformed); it names the resource for whoever reads the error in a
// test or a log.
func patchNotAnObject(noun string) error {
	return fmt.Errorf("%s patch must be a JSON object", noun)
}

// decodePatch strictly decodes one JSON object into a new F and returns the
// json names of F's fields the object set to null, sorted.
//
// F is meant to be a PATCH body type redeclared without its methods (`type
// fields organizationPatchDTO`), so decoding into it does not recurse back
// into the UnmarshalJSON that called this. noun names the resource in the
// not-an-object error, as patchNotAnObject says.
func decodePatch[F any](data []byte, noun string) (F, []string, error) {
	var decoded F
	nulls, err := decodePatchObject(data, &decoded, reflect.TypeOf(decoded), noun)
	return decoded, nulls, err
}

// decodePatchObject strictly decodes one JSON object into dst, and returns
// the json names of shape's fields that the object set to null, sorted.
//
// decodeJSON's DisallowUnknownFields does not reach inside a custom
// unmarshaler, so the strict decode is repeated here; an unknown key is still
// refused as a malformed body.
//
// The names returned are the field's own tags rather than the keys as sent.
// encoding/json matches a key to a field without regard to case, so the key
// as sent could be any spelling of it, and echoing caller bytes into an
// error is the reflection shape FAILURE_PATTERNS 72 records.
func decodePatchObject(data []byte, dst any, shape reflect.Type, noun string) ([]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		// The literal null: Unmarshal leaves the map nil without an error.
		return nil, patchNotAnObject(noun)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return nil, err
	}
	if field, repeated := repeatedPatchField(data, shape); repeated {
		return nil, &patchRepeatedFieldError{Field: field}
	}

	var nulls []string
	for key, value := range raw {
		if string(bytes.TrimSpace(value)) != "null" {
			continue
		}
		nulls = append(nulls, patchFieldName(shape, key))
	}
	sort.Strings(nulls)
	return nulls, nil
}

// patchRepeatedFieldError is the refusal of a PATCH body that names one of
// its fields twice, in any mix of upper and lower case.
//
// encoding/json matches a key to a field without regard to case and lets
// the later key win, and a field decoded by its own unmarshaler into a
// pointer it already set (a credential's external, a template's survey)
// decodes the second value into the first, so a body naming external twice
// used to store both sets of references. Which one the caller meant cannot
// be known, so neither is applied. decodeJSON answers it with its own text,
// which names the field by its tag and repeats nothing the caller sent.
type patchRepeatedFieldError struct {
	// Field is the field's json name, prefixed by the object it is in when
	// that object is nested ("survey.enabled").
	Field string
}

// Error says which field was named twice and what to do instead.
func (e *patchRepeatedFieldError) Error() string {
	return `"` + e.Field + `" is named more than once in the body, in some mix of upper and lower case; name each field once`
}

// repeatedPatchField returns the json name of the first of shape's fields
// that the top level of the object in data names twice, matching keys to
// fields as encoding/json does, and whether there is one. It runs after the
// strict decode has refused an unknown key, so every key here names a field.
func repeatedPatchField(data []byte, shape reflect.Type) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return "", false
	}
	seen := make(map[string]bool)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return "", false
		}
		key, _ := token.(string)
		name := patchFieldName(shape, key)
		if seen[name] {
			return name, true
		}
		seen[name] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return "", false
		}
	}
	return "", false
}

// patchFieldName finds the json name of the field in shape that key decodes
// into, matching as encoding/json does, without regard to case.
//
// The strict decode has already refused a key that matches no field, so the
// fallback is never expected to be reached. It is a fixed phrase rather than
// the key, so even then nothing the caller sent is echoed, and the null is
// still refused rather than skipped.
func patchFieldName(shape reflect.Type, key string) string {
	name := "an unrecognized field"
	for i := range shape.NumField() {
		tag, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		if tag != "" && strings.EqualFold(tag, key) {
			name = tag
			break
		}
	}
	return name
}

// patchNullRefusal is the 400 message for a PATCH body that set the named
// fields to null. noun is the resource with its article, such as "a
// template" or "an organization".
func patchNullRefusal(noun string, nulls []string) string {
	quoted := make([]string, len(nulls))
	for i, name := range nulls {
		quoted[i] = `"` + name + `"`
	}
	return noun + " field may not be null: " + strings.Join(quoted, ", ") +
		"; leave a field out to keep it, or send an empty value to clear it"
}

// fixedFieldMessage is the 400 message for a PATCH that asks to change a
// field no update can change. noun is the resource with its article, and
// remedy, when not empty, says what to do instead ("copy the template
// instead").
func fixedFieldMessage(field, noun, remedy string) string {
	msg := field + " cannot be changed: " + noun + "'s " + field + " is fixed once it exists"
	if remedy != "" {
		msg += "; " + remedy
	}
	return msg
}

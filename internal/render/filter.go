package render

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// The filter set is closed, and closing it is a security decision rather
// than a scoping one.
//
// Jinja2 ships around fifty filters, several of which read the filesystem,
// execute code, or perform unbounded work on caller-controlled input. This
// renderer's input arrives from an API-writable database row and its output
// is injected into a process that handles secrets, so every filter here had
// to earn its place by appearing in a real AWX credential type. Adding one
// is a deliberate act with a written reason, not a convenience.
//
// Deliberately excluded, each for a stated reason:
//
//	regex_replace  a caller-supplied pattern is unbounded work on a large
//	               input even under RE2, which has no backtracking but does
//	               not bound the input size
//	password_hash  a work factor a caller controls is a denial of service
//	from_yaml      a second deserializer on a path that already has one
//	lookup         reads the filesystem and the environment by design
//	map, select    require a callable, which this grammar has no way to
//	               express, so supporting them means growing the grammar
const (
	filterDefault   = "default"
	filterQuote     = "quote"
	filterToJSON    = "to_json"
	filterToJSONAlt = "tojson"
	filterB64Encode = "b64encode"
	filterB64Decode = "b64decode"
	filterLower     = "lower"
	filterUpper     = "upper"
	filterTrim      = "trim"
)

// filterSpec is one filter's arity and behavior.
type filterSpec struct {
	// minArgs and maxArgs bound the literal argument list. Checking arity
	// at compile time is what lets a credential type's author learn about
	// a miswritten filter when they save the type.
	minArgs int
	maxArgs int

	// apply transforms a value. It is nil for default, which the evaluator
	// handles itself because it acts on the undefined state rather than on
	// a value.
	apply func(value any, args []any) (any, error)
}

// filters is the closed set. A name absent from this map is ErrUnknownFilter.
var filters = map[string]filterSpec{
	// default is the only escape from the strict-undefined rule, which is
	// why it takes exactly one argument: an author declaring an input
	// optional must say what it falls back to. It is handled in eval.go
	// rather than here, so apply stays nil.
	filterDefault: {minArgs: 1, maxArgs: 1},

	// quote wraps a value in POSIX single quotes so it can be embedded in
	// a shell-sourced file without the shell reinterpreting it. AWX's own
	// types use it for values that land in an rc file.
	filterQuote: {apply: applyQuote},

	// to_json is what makes a nested extra-var expressible. Without it a
	// structure has no string form, because Go's default formatting of a
	// map is not valid input to anything.
	filterToJSON:    {apply: applyToJSON},
	filterToJSONAlt: {apply: applyToJSON},

	// b64encode and b64decode appear in AWX's kubernetes and gpg types,
	// where a certificate or a key travels base64-wrapped.
	filterB64Encode: {apply: applyB64Encode},
	filterB64Decode: {apply: applyB64Decode},

	// lower, upper and trim are total, allocation-bounded, and present in
	// real AWX types that normalize a region name or strip a pasted
	// trailing newline.
	filterLower: {apply: applyLower},
	filterUpper: {apply: applyUpper},
	filterTrim:  {apply: applyTrim},
}

// isKnownFilter reports whether name is in the closed set.
func isKnownFilter(name string) bool {
	_, ok := filters[name]
	return ok
}

// checkFilterArgs validates one filter call's arity at compile time.
func checkFilterArgs(f filterCall) error {
	spec := filters[f.name]
	if len(f.args) < spec.minArgs {
		return fmt.Errorf("%w: filter %q needs %d argument(s), got %d", ErrSyntax, f.name, spec.minArgs, len(f.args))
	}
	if len(f.args) > spec.maxArgs {
		return fmt.Errorf("%w: filter %q takes at most %d argument(s), got %d", ErrSyntax, f.name, spec.maxArgs, len(f.args))
	}
	return nil
}

// applyQuote wraps value in POSIX single quotes.
//
// The embedded-quote case is the one that matters: a single quote inside a
// single-quoted shell string cannot be escaped, so the standard idiom ends
// the string, emits an escaped quote, and starts a new one. A password
// containing an apostrophe is common enough that getting this wrong is a
// real outage rather than a theoretical one.
func applyQuote(value any, _ []any) (any, error) {
	s, err := text(value)
	if err != nil {
		return nil, err
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", nil
}

// applyToJSON encodes value as JSON.
//
// SetEscapeHTML is turned off because the default escaping of <, > and &
// is a browser concern, and this output goes into an environment variable
// or a generated file where the escaped form is simply wrong.
func applyToJSON(value any, _ []any) (any, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("%w: to_json could not encode the value: %s", ErrNotRenderable, err)
	}
	// Encode appends a newline that no caller wants in an injected value.
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// applyB64Encode base64-encodes value's text form.
func applyB64Encode(value any, _ []any) (any, error) {
	s, err := text(value)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}

// applyB64Decode base64-decodes value's text form.
//
// A decode failure is an error rather than a passthrough. Passing the
// undecoded text through would inject a value that looks plausible and
// authenticates against nothing, which is the failure shape hardest to
// diagnose from the other end.
func applyB64Decode(value any, _ []any) (any, error) {
	s, err := text(value)
	if err != nil {
		return nil, err
	}
	decoded, decErr := base64.StdEncoding.DecodeString(s)
	if decErr != nil {
		// The error text names neither the input nor the decoder's own
		// message, both of which quote the offending bytes, and the
		// offending bytes here are a secret that failed to decode.
		return nil, fmt.Errorf("%w: b64decode input is not valid base64", ErrNotRenderable)
	}
	return string(decoded), nil
}

// applyLower lowercases value's text form.
func applyLower(value any, _ []any) (any, error) {
	s, err := text(value)
	if err != nil {
		return nil, err
	}
	return strings.ToLower(s), nil
}

// applyUpper uppercases value's text form.
func applyUpper(value any, _ []any) (any, error) {
	s, err := text(value)
	if err != nil {
		return nil, err
	}
	return strings.ToUpper(s), nil
}

// applyTrim strips leading and trailing whitespace from value's text form.
func applyTrim(value any, _ []any) (any, error) {
	s, err := text(value)
	if err != nil {
		return nil, err
	}
	return strings.TrimSpace(s), nil
}

// Package secretref reads and resolves secret references,
// {{ secret.<name>.<field> }}, in the text of a parameter a method declares
// takes them (collection.Param.SecretRefs). The engine never renders a
// reference: the method resolves it here, from the project's named secrets
// (sdk.NamedSecrets), just before the text reaches the device, and masks the
// value it substituted in everything it returns.
//
// The grammar is deliberately small. A reference is the whole expression,
// with no filter and nothing else inside the braces, and a string holding one
// holds no other template expression, so nothing data supplies can sit next
// to a secret or choose which one is read.
package secretref

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Root is the template root a reference reads.
const Root = "secret"

// Ref names one field of one named secret.
type Ref struct {
	Name  string
	Field string
}

// String is the reference as a runbook writes it.
func (r Ref) String() string {
	return "{{ " + Root + "." + r.Name + "." + r.Field + " }}"
}

// refPattern is one well formed reference: a name and a field, each a
// lowercase identifier (credential.ValidSecretName's rule), with optional
// spaces inside the braces and nothing else.
var refPattern = regexp.MustCompile(`\{\{\s*secret\.([a-z][a-z0-9_]{0,63})\.([a-z][a-z0-9_]{0,63})\s*\}\}`)

// looseRef is anything that opens an expression reading the secret root,
// well formed or not, so a malformed reference is refused rather than passed
// to the renderer or to a device as literal text.
var looseRef = regexp.MustCompile(`\{\{-?\s*secret\b`)

// Has reports whether text holds anything that reads the secret root, well
// formed or not.
func Has(text string) bool {
	return looseRef.MatchString(text)
}

// Find returns the well formed references in text, in order, repeats
// included.
func Find(text string) []Ref {
	var out []Ref
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		out = append(out, Ref{Name: m[1], Field: m[2]})
	}
	return out
}

// Problems returns what is wrong with text as a string holding secret
// references, or nothing: a reference that is not exactly
// {{ secret.<name>.<field> }}, and any other template expression beside one.
// A string with no reference has no problems here.
func Problems(text string) []string {
	if !Has(text) {
		return nil
	}
	var problems []string
	rest := refPattern.ReplaceAllString(text, "")
	if looseRef.MatchString(rest) {
		problems = append(problems, "a secret reference is written {{ secret.<name>.<field> }}, a lowercase name and field, with no filter")
	}
	if strings.Contains(rest, "{{") {
		problems = append(problems, "a string holding a secret reference may hold no other template expression, so nothing a run supplies sits next to a secret")
	}
	return problems
}

// ErrUnsafeValue is returned for a secret whose value could change the
// command it is substituted into.
var ErrUnsafeValue = errors.New("the secret's value holds whitespace, a control character or a ?, which a network device's command line would read as more than one value or as a request for help")

// CheckValue refuses a value that could change the meaning of the command
// line it lands in: whitespace (a second argument), a control character (a
// second command), and ? (a network CLI's help, which echoes the line).
func CheckValue(value string) error {
	if value == "" {
		return errors.New("the secret's value is empty")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '?' || r == unicode.ReplacementChar {
			return ErrUnsafeValue
		}
	}
	return nil
}

// Lookup returns the value of one reference, and false when there is no such
// secret or field.
type Lookup func(Ref) (string, bool, error)

// Resolve returns text with each reference replaced by its value, and the
// values it substituted, for the caller to mask. A malformed reference, a
// reference to a secret that does not exist, and a value CheckValue refuses
// are errors naming the reference, never its value.
func Resolve(text string, lookup Lookup) (string, []string, error) {
	if problems := Problems(text); len(problems) > 0 {
		return "", nil, errors.New(problems[0])
	}
	var values []string
	var firstErr error
	out := refPattern.ReplaceAllStringFunc(text, func(m string) string {
		if firstErr != nil {
			return m
		}
		sub := refPattern.FindStringSubmatch(m)
		ref := Ref{Name: sub[1], Field: sub[2]}
		value, ok, err := lookup(ref)
		switch {
		case err != nil:
			firstErr = fmt.Errorf("%s: %w", ref, err)
			return m
		case !ok:
			firstErr = fmt.Errorf("%s: no such secret or field in this project (pleiades add-secret %s --field %s --generate 32, or --value-stdin for a value you have)", ref, ref.Name, ref.Field)
			return m
		}
		if err := CheckValue(value); err != nil {
			firstErr = fmt.Errorf("%s: %w", ref, err)
			return m
		}
		values = append(values, value)
		return value
	})
	if firstErr != nil {
		return "", nil, firstErr
	}
	return out, values, nil
}

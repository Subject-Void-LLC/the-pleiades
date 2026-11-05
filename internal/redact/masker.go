package redact

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Masker applies one Ruleset. It is the only masking engine in this
// codebase, and it is safe for concurrent use.
//
// Construct it once per process in the composition root and hand the same
// instance to everything that logs, so every terminal writer carries the
// same rules. internal/archtest fails the build if any cmd/ binary builds a
// slog handler without one.
type Masker struct {
	// keys are the KindKey names, lowercased once at construction so the
	// per-attribute comparison is a map lookup rather than a scan.
	keys map[string]struct{}

	// patterns are the compiled KindPattern rules, in ruleset order, each
	// with the cheap substring test that decides whether to run it.
	patterns []compiledPattern

	// literals is the process-wide by-value channel.
	literals *Literals
}

// NewMasker compiles a ruleset into an engine.
//
// It returns an error rather than panicking because a Ruleset can come from
// somewhere other than the embedded file, and a composition root should
// refuse to start on a bad one rather than crash mid-flight.
func NewMasker(rs Ruleset) (*Masker, error) {
	if err := rs.Validate(); err != nil {
		return nil, err
	}

	m := &Masker{
		keys:     make(map[string]struct{}),
		literals: newLiterals(),
	}

	for _, r := range rs.Rules {
		switch r.Kind {
		case KindKey:
			for _, k := range r.Keys {
				m.keys[strings.ToLower(k)] = struct{}{}
			}
		case KindPattern:
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("redact: rule %q has an invalid pattern: %w", r.Name, err)
			}
			m.patterns = append(m.patterns, compiledPattern{
				name:      r.Name,
				re:        re,
				prefilter: r.Prefilter,
			})
		}
	}

	return m, nil
}

// Literals returns the process-wide set of exact secret values.
//
// Whatever injects a secret registers it here, and every masked line
// afterwards scrubs it. Handing out the set rather than taking it at
// construction means a composition root wires one object instead of two,
// and there is no way to build a Masker whose literal channel points
// somewhere other than the one its Attr method reads.
func (m *Masker) Literals() *Literals { return m.literals }

// Text masks free text.
//
// extra is masked alongside the process-wide literal set, for the caller
// that knows a secret this line might contain and has not registered it
// globally: a per-dispatch credential whose life is one Execute call, for
// example. Passing nil is normal.
//
// Key rules do not apply here, because free text has no attribute name to
// match. Pattern rules do.
func (m *Masker) Text(extra []string, text string) string {
	if text == "" {
		return text
	}

	// Literals first. A pattern rule replaces its match with the
	// placeholder, so running patterns first could destroy the exact bytes
	// a literal was going to match and leave a partially masked value.
	literals := m.literals.snapshotShared()
	if len(extra) > 0 {
		// The cached slice is shared, so this must not append into it.
		// Combining the two sources into one list before masking is what
		// keeps the longest-first rule applying ACROSS them: masking them
		// separately would let a caller-supplied password that is a prefix
		// of a registered passphrase carve the passphrase in half and leave
		// its tail exposed.
		combined := make([]string, 0, len(literals)+len(extra))
		combined = append(combined, literals...)
		combined = append(combined, extra...)
		literals = combined
	}
	// A device that echoes the line it was sent (a login shell's trace, a
	// ForceCommand's log) echoes each secret in it as a connection encoded
	// it for sh (pkg/remoteexec/shline), whose escapes all begin \0; such
	// text is held to those forms too.
	if strings.Contains(text, `\0`) {
		encoded := make([]string, 0, 2*len(literals))
		encoded = append(encoded, literals...)
		for _, l := range literals {
			if e := shEncoded(l); e != l {
				encoded = append(encoded, e)
			}
		}
		literals = encoded
	}
	out := maskLiterals(literals, text)

	// The prefilter pass. Lowercasing once and testing cheap substrings is
	// dramatically faster than running every regular expression on every
	// line, and almost every line contains no secret shape at all. The
	// lowercase copy is built lazily, so a process with no pattern rules
	// never pays for it.
	var lowered string
	for i := range m.patterns {
		p := &m.patterns[i]
		if len(p.prefilter) > 0 {
			if lowered == "" {
				lowered = strings.ToLower(out)
			}
			if !containsAny(lowered, p.prefilter) {
				continue
			}
		}
		before := out
		out = p.re.ReplaceAllString(out, maskPlaceholder)
		if out != before {
			// A replacement changed the text, so the lowercase copy is
			// stale for every rule after this one. Rebuilding is correct
			// and rare: it only happens on a line that actually contained
			// a secret.
			lowered = ""
		}
	}
	return out
}

// compiledPattern is one pattern rule ready to run.
type compiledPattern struct {
	// name identifies the rule in a failure. It is unused on the hot path
	// and exists so a future diagnostic can say which rule fired.
	name string

	re *regexp.Regexp

	// prefilter is the rule's cheap substring test, lowercase. Empty means
	// the pattern always runs.
	prefilter []string
}

// containsAny reports whether lowered contains at least one of subs.
func containsAny(lowered string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(lowered, sub) {
			return true
		}
	}
	return false
}

// Attr is a slog.HandlerOptions.ReplaceAttr function.
//
// It is the whole reason this package exposes a function rather than a
// wrapping slog.Handler. See the package comment for why a wrapper cannot
// do this job: it cannot see attributes added through Logger.With, and it
// sees the message only as an opaque string.
//
// Four things happen here, in order:
//
//  1. A group is returned untouched. The standard library recurses into a
//     group's leaves and calls this again for each one, with the group's
//     name in groups.
//  2. An attribute whose NAME matches a key rule has its value replaced by
//     Marker outright, whatever the value is. This is the channel that
//     catches a secret this process does not know the bytes of. So is one
//     inside a group whose name matches (a group named "password" holds
//     secrets in its leaves "old" and "new"), unless its own key names an
//     identifier (identifierKeys): a credential group's username is shown.
//  3. A string value, including the built-in msg, is run through Text.
//  4. A value of a kind that cannot carry text (a number, a bool, a
//     duration, a time, the built-in level and source) is returned
//     untouched. A map, a list or a struct (not an error, which a handler
//     prints by its text) is masked as Value masks a method's stats, key
//     rules included, so {"password": ...} in a map or a Password field
//     is masked by name as a top-level attribute is, and the attribute
//     becomes the masked value, keeping its shape, when that changes
//     anything. Any other value (an error, a []byte, a Stringer, a named
//     string type, a struct, a map) is rendered the way a handler prints it,
//     and when masking that text changes it the attribute becomes the masked
//     text: a handler would otherwise print an error quoting a device's
//     output, or marshal a struct whole, without Text ever seeing it. A
//     value whose rendering masking leaves alone is returned untouched, so
//     the structured output keeps its shape wherever nothing is secret.
//
// The attribute's key is never changed. Rendering calls a value's Error,
// String and MarshalJSON methods whichever handler is in use, so a value
// logged here must not take, in one of them, a lock its logging caller may
// hold.
func (m *Masker) Attr(groups []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		return a
	}

	if m.secretName(a.Key) || (slices.ContainsFunc(groups, m.secretName) && !shownIdentifier(a)) {
		return slog.String(a.Key, Marker)
	}

	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, m.Text(nil, a.Value.String()))
	case slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool, slog.KindDuration, slog.KindTime:
		return a
	}
	value := a.Value.Any()
	switch value.(type) {
	case nil, slog.Level, *slog.Source:
		return a
	}
	if masked, changed := m.structured(value); changed {
		return slog.Any(a.Key, masked)
	}
	shown, others := renderings(value)
	for _, rendered := range append([]string{shown}, others...) {
		if m.Text(nil, rendered) != rendered {
			return slog.String(a.Key, m.Text(nil, shown))
		}
	}
	return a
}

// structured masks value as Value masks stats when it is a map, a list or
// a struct (or a pointer to one) other than an error or a byte slice,
// reporting whether masking changed it. Value walks a struct as the maps
// JSON writes it as, so the comparison is with that form. A value that
// cannot be marshalled (a panicking MarshalJSON among them, which Value's
// conversion would not survive) is left to the text renderings.
func (m *Masker) structured(value any) (any, bool) {
	if _, isErr := value.(error); isErr {
		return nil, false
	}
	v := reflect.Indirect(reflect.ValueOf(value))
	switch v.Kind() {
	case reflect.Map, reflect.Struct, reflect.Array:
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return nil, false
		}
	default:
		return nil, false
	}
	if _, ok := marshalJSON(value); !ok {
		return nil, false
	}
	masked := m.Value(nil, value)
	if reflect.DeepEqual(masked, value) || reflect.DeepEqual(masked, generic(value)) {
		return nil, false
	}
	return masked, true
}

// shownIdentifier reports whether a, inside a secret-named group, is an
// identifier that stays shown: an identifier key (identifierKeys) holding a
// string or a scalar. A struct or a map under such a key can hold anything.
func shownIdentifier(a slog.Attr) bool {
	if !identifierKeys[strings.ToLower(a.Key)] {
		return false
	}
	switch a.Value.Kind() {
	case slog.KindString, slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool:
		return true
	}
	return false
}

// identifierKeys are the leaf keys that name an identifier, not a secret,
// and stay shown inside a secret-named group: who and where, never what
// proves it.
var identifierKeys = map[string]bool{
	"username": true, "user": true, "login": true, "name": true,
	"host": true, "hostname": true, "address": true, "port": true, "id": true,
}

// secretName reports whether name matches a key rule, whatever its case.
func (m *Masker) secretName(name string) bool {
	_, secret := m.keys[strings.ToLower(name)]
	return secret
}

// renderings is every text a handler may print for value. shown is the one
// a masked attribute is replaced with: a byte slice's bytes as text (of any
// named byte-slice type, json.RawMessage among them), otherwise TextHandler's
// %+v, which for a byte slice would be its decimal bytes, the secret in
// another spelling. others are the rest a handler may print, all checked:
// the %+v, and JSONHandler's encoding, which for a []byte is base64 and for a
// struct is its fields. fmt recovers a panicking Error or String method into
// its own text; a panicking MarshalJSON is recovered here and that rendering
// skipped, since ReplaceAttr runs outside slog's own recovery and a panic
// there would end the logging goroutine.
func renderings(value any) (shown string, others []string) {
	formatted := fmt.Sprintf("%+v", value)
	shown = formatted
	if v := reflect.ValueOf(value); v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
		shown = string(v.Bytes())
		others = append(others, formatted)
	}
	if encoded, ok := marshalJSON(value); ok {
		others = append(others, encoded)
	}
	return shown, others
}

// marshalJSON is json.Marshal with a panic in a MarshalJSON method recovered
// into a failure.
func marshalJSON(value any) (encoded string, ok bool) {
	defer func() {
		if recover() != nil {
			encoded, ok = "", false
		}
	}()
	b, err := json.Marshal(value)
	return string(b), err == nil
}

// HandlerOptions returns slog handler options already carrying Attr.
//
// This exists so a caller cannot build a handler that holds the ruleset and
// forgot to install it. Every cmd/ binary constructs its handler from this
// rather than from a literal &slog.HandlerOptions{}, and
// internal/archtest's TestEverySlogHandlerCarriesTheMaskingRuleset fails
// the build when one does not.
func (m *Masker) HandlerOptions(level slog.Leveler) *slog.HandlerOptions {
	return &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: m.Attr,
	}
}

// shEncoded is text as pkg/remoteexec/shline.Encode writes it: each byte
// outside letters, digits, a space and _ . / : , = + - @ % as \0 and three
// octal digits. It is a copy, since this package depends on nothing but the
// standard library (internal/archtest); TestMasking_EncodedSecretsMatchShline
// holds the two equal.
func shEncoded(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(" _./:,=+-@%", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('\\')
		b.WriteByte('0')
		b.WriteByte('0' + c>>6)
		b.WriteByte('0' + c>>3&7)
		b.WriteByte('0' + c&7)
	}
	return b.String()
}

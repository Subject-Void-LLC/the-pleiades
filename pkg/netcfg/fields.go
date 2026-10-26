// Secret fields in structured output: the keys of a parsed command's result
// (net.ios.parse, through pkg/iosparse's port of genieparser) whose values are
// secrets. The names are genieparser's own, so Genie's schemas say what a
// field holds; this table is the one place that decides which of them are
// secrets, beside the configuration words mask.go reads.
package netcfg

import "strings"

// secretFields are the field names whose values are secrets, as
// genieparser names them in its schemas: a password or secret, a key string,
// an IKE pre-shared key, an SNMP community, a routing protocol's
// authentication key, a passphrase.
var secretFields = map[string]bool{
	"password":           true,
	"secret":             true,
	"enable_password":    true,
	"enable_secret":      true,
	"key_string":         true,
	"pre_shared_key":     true,
	"preshared_key":      true,
	"shared_key":         true,
	"community":          true,
	"community_string":   true,
	"authentication_key": true,
	"auth_key":           true,
	"md5_key":            true,
	"passphrase":         true,
}

// SecretField reports whether name, a parsed result's key, holds a secret.
func SecretField(name string) bool {
	return secretFields[strings.ToLower(name)]
}

// MaskFields returns a copy of v, a parsed result of nested maps and lists,
// with every text value under a secret field replaced by its placeholder, as
// Mask replaces one in configuration text; equal secrets get equal
// placeholders, so two reads still compare. Every other value is kept.
func MaskFields(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			if s, ok := e.(string); ok && SecretField(k) && s != "" && !strings.HasPrefix(s, MaskPrefix) {
				out[k] = Placeholder(s)
				continue
			}
			out[k] = MaskFields(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = MaskFields(e)
		}
		return out
	}
	return v
}

// MinLearnedLength is the shortest secret MaskFound's caller should add to a
// run's masking set: shorter values are masked where a secret word precedes
// them, and only there, since masking a short or common word ("cisco") by
// value everywhere scrubs ordinary output too. It is the engine's own minimum
// for a value marked secret (redact.MinLiteralLength), which a pkg/ package
// cannot import.
const MinLearnedLength = 8

// Learnable returns the values of found long enough to mask by value.
func Learnable(found []string) []string {
	var out []string
	for _, f := range found {
		if len(f) >= MinLearnedLength {
			out = append(out, f)
		}
	}
	return out
}

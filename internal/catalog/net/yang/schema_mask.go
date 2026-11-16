// Package yang: masking a YANG source by value, and only by value. The
// configuration masker (pkg/netcfg's MaskOutput) reads a value's shape: the
// word after "key" or "password", a crypt-shaped string. In a schema those are
// grammar (a list's key statement, a leaf named password), and running a
// source through it is the defect CVD run 11 found, 481 words of three
// modules turned into placeholders. So a source is masked by the run's known
// secret values alone, each replaced wherever it stands, and is otherwise
// kept byte for byte.
package yang

import (
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// minMaskLength is the shortest value a source is masked for: the engine's
// own minimum for a value masked as a secret wherever it stands
// (netcfg.MinLearnedLength, which is redact.MinLiteralLength). A shorter one
// (a password "admin") replaced everywhere would cut words of the schema
// apart ("administrative", admin-status), which is the corruption this
// method exists to avoid. So a short value is never masked: a source holding
// one as a whole token (holdsToken) is refused, naming the module and never
// the value, and nothing is written, since a file of the project is no place
// to leave a secret in the clear.
const minMaskLength = netcfg.MinLearnedLength

// placeholder stands in for every known secret a saved source held: one
// fixed word, the same in every run and for every secret, so a re-run against
// an unchanged device writes nothing, and a file says nothing of which secret
// stood where or how long it was. netcfg.Placeholder, which a run's report
// uses, is keyed afresh in every process, so a file masked with it would
// differ on every run (the release gate's second run found this).
const placeholder = netcfg.MaskPrefix + "secret"

// maskForms is what a source is masked by: the values of the device's
// credential output must never show (wire.MaskedValues, the rule every path
// masks a credential by) and the values the run masks from its start (the
// project's named secrets, sdk.KnownSecrets), each once, as it is and as a
// YANG double-quoted string writes it (yangQuoted), longest first. A value
// shorter than minMaskLength is never masked; it is returned in shorts, with
// its quoted form, for refusesShort to look for.
func maskForms(rc sdk.RunbookContext) (forms, shorts []string) {
	seen := map[string]bool{}
	for _, v := range append(wire.MaskedValues(rc.InjectSecrets()), sdk.KnownSecretValues(rc)...) {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		into := &forms
		if len(v) < minMaskLength {
			into = &shorts
		}
		*into = append(*into, v)
		if q := yangQuoted(v); !seen[q] {
			seen[q] = true
			*into = append(*into, q)
		}
	}
	// Longest first, so where one form holds another the longer is
	// replaced whole; then by text, so the order is the same every run.
	sort.Slice(forms, func(i, j int) bool {
		if len(forms[i]) != len(forms[j]) {
			return len(forms[i]) > len(forms[j])
		}
		return forms[i] < forms[j]
	})
	return forms, shorts
}

// holdsToken reports whether text holds v as a whole token: an occurrence
// with no character of a YANG identifier ([A-Za-z0-9_.-]) directly before or
// after it. A short secret inside a longer word ("admin" in admin-status or
// administrative) is the schema's word, not the secret.
func holdsToken(text, v string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], v)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(v)
		if (start == 0 || !tokenByte(text[start-1])) && (end == len(text) || !tokenByte(text[end])) {
			return true
		}
		from = start + 1
	}
}

// tokenByte reports whether b is a character of a YANG identifier.
func tokenByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '.' || b == '-'
}

// yangQuoted is v as the inside of a YANG double-quoted string writes it
// (RFC 7950 section 6.1.3): a backslash, a double quote, a newline and a tab
// each escaped, so a secret holding one is found where a module quotes it.
func yangQuoted(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(v)
}

// maskKnown is src with each form replaced by placeholder wherever it
// stands, in one pass over src that never reads its own output, and whether
// anything was replaced. With nothing to replace it is src itself, byte for
// byte.
func maskKnown(src []byte, forms []string) ([]byte, bool) {
	if len(forms) == 0 {
		return src, false
	}
	pairs := make([]string, 0, 2*len(forms))
	for _, f := range forms {
		pairs = append(pairs, f, placeholder)
	}
	// A Replacer matches its old strings in the order given at each place in
	// the text, so the longest form standing there wins.
	text := string(src)
	masked := strings.NewReplacer(pairs...).Replace(text)
	if masked == text {
		return src, false
	}
	return []byte(masked), true
}

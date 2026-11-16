// Masking a pattern rule's capturing groups alone (Rule.SecretGroups).
package redact

import (
	"regexp"
	"strings"
)

// netcfgPlaceholderPrefix begins every placeholder pkg/netcfg writes for a
// secret it masked in a device's configuration (netcfg.MaskPrefix, held
// equal to this by a test). Such a placeholder is comparable (equal secrets
// read alike) and is a secret masked already, so a group holding one is
// kept rather than masked a second time into a placeholder that compares
// nothing.
const netcfgPlaceholderPrefix = "pleiades-masked-"

// maskGroups replaces each capturing group of every match of re in text with
// the placeholder, keeping the rest of each match, and keeping a group that
// already holds a placeholder. A group nested inside one replaced already is
// skipped, since its text is gone.
func maskGroups(re *regexp.Regexp, text string) string {
	matches := re.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, m := range matches {
		// m holds the whole match's bounds and then each group's; a group
		// that took no part in the match is -1.
		for g := 2; g+1 < len(m); g += 2 {
			start, end := m[g], m[g+1]
			if start < 0 || start < last || maskedAlready(text[start:end]) {
				continue
			}
			b.WriteString(text[last:start])
			b.WriteString(maskPlaceholder)
			last = end
		}
	}
	b.WriteString(text[last:])
	return b.String()
}

// maskedAlready reports whether a group's text is a placeholder: this
// package's own, or pkg/netcfg's, quoted or not. Keeping one hides nothing:
// each prefix is at least MinLiteralLength long, so a real secret that began
// with one would be long enough to be masked by value, and shape is never
// the only masking such a secret gets.
func maskedAlready(group string) bool {
	group = strings.TrimPrefix(group, `"`)
	return strings.HasPrefix(group, maskPlaceholder) || strings.HasPrefix(group, netcfgPlaceholderPrefix)
}

// The bounds every fact a device reports is cut to before it is kept.
package onboard

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds on what a probe keeps from a device's answers. A device is not
// trusted to be brief: every text fact is cut to maxFactText bytes and
// every list to maxFactList entries before it reaches the inventory.
const (
	maxFactText = 256
	maxFactList = 256
)

// factText makes s safe to store: valid UTF-8, no control or format
// characters (terminal escapes, bidirectional overrides), and at most
// maxFactText bytes. The terminal and the API escape again on output;
// this is the inventory's own bound.
func factText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(s, "") {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > maxFactText {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// factList bounds a list of text facts, dropping entries that are empty
// once made safe.
func factList(items []string) []any {
	out := make([]any, 0, min(len(items), maxFactList))
	for _, it := range items {
		if len(out) == maxFactList {
			break
		}
		if s := factText(it); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Package facts: a stored key as a person reads it (ShownKey).
package facts

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// ShownKey is key as a report shows it: as it is when a new fact could be
// named it (ValidKey), and otherwise with every character that would keep it
// from showing as itself written as \u{XXXX} (its code point in hex), every
// byte that is not UTF-8 as \x{XX}, and every backslash as \u{5C}, so the
// shown form never reads as another key and cannot be mistaken for a key
// holding that text.
//
// The reader's rule (wellFormedKey) is frozen and reads keys ValidKey
// refuses: one holding a code point a newer toolchain assigned, which a
// writer of that build could store, and, from a hand-edited store, one
// holding a zero width or format character, a line separator, a private-use
// character or a space other than U+0020. A volatile key's file holds any
// string at all, a newline among them. "." and "..", which ValidKey refuses
// only as a URL segment, show as themselves.
func ShownKey(key string) string {
	if ValidKey(key) {
		return key
	}
	var b strings.Builder
	for i := 0; i < len(key); {
		r, size := utf8.DecodeRuneInString(key[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x{%02X}`, key[i])
		case shownAsItself(r):
			b.WriteString(key[i : i+size])
		default:
			fmt.Fprintf(&b, `\u{%X}`, r)
		}
		i += size
	}
	return b.String()
}

// shownAsItself reports whether r, alone, shows as itself in a key: a
// printable character other than U+0020 and the backslash that a name may
// hold (recordname.ShowsAsItself).
func shownAsItself(r rune) bool {
	return r != ' ' && r != '\\' && unicode.IsPrint(r) && recordname.ShowsAsItself(string(r)) == nil
}

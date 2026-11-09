// Package recordname: which characters keep a name from showing as itself,
// written out as fixed code points so the rule reads the same under every
// Go release.
package recordname

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// shownAsItself returns an error naming the first byte or character that
// would keep name from showing as itself on one line, or nil. The error
// names a character by its code point and byte offset and never quotes
// name.
//
// What it refuses, and why:
//
//   - Invalid UTF-8. A reader shows a byte that is not UTF-8 as U+FFFD, so
//     two different names would print, and could be matched, as one
//     (FAILURE_PATTERNS 932).
//   - Every control code: C0 (U+0000 to U+001F, newline, carriage return,
//     tab and escape among them), DEL (U+007F) and C1 (U+0080 to U+009F,
//     NEL among them). A terminal or a log acts on these instead of showing
//     them: a newline starts a line that looks like the program's own.
//   - The direction controls: U+061C, U+200E, U+200F, U+202A to U+202E and
//     U+2066 to U+2069 (the marks, embeddings, overrides and isolates). They
//     change the order a name is displayed in, so "abc" followed by an
//     override can show as another name ("Trojan Source"). No script needs
//     one inside a name: Arabic and Hebrew letters order themselves, and the
//     controls exist for mixing directions in running text.
//   - The line and paragraph separators, U+2028 and U+2029. A browser
//     breaks a line at each, so the UI would show the name as two lines.
//   - The invisible characters a name picks up by accident: U+00AD (soft
//     hyphen), U+200B (zero width space), U+2060 (word joiner) and U+FEFF
//     (the byte order mark a file can start with). Each shows as nothing,
//     so the name looks like the same name without it and never matches
//     what an operator types. None has a job inside a name.
//   - The tag characters, U+E0000 to U+E007F. They show as nothing but
//     spell out ASCII text, which a program or a model reading the name
//     sees and a person does not. Their one other use, a regional flag
//     emoji, has no place in a device name.
//
// What it keeps, and why:
//
//   - U+200C and U+200D, the zero width non-joiner and joiner. Persian,
//     Urdu and the Indic scripts need them to spell words correctly, and
//     emoji sequences are built with them.
//   - The variation selectors (U+FE00 to U+FE0F, U+E0100 to U+E01EF):
//     an emoji's color form and a Chinese or Japanese character's variant
//     need them.
//   - Spaces, letters with accents, and every other letter, mark, digit,
//     punctuation mark and symbol.
//
// It does not try to stop two names that look alike: a Latin "a" and a
// Cyrillic one, or a name and the same name with a space after it, look
// the same, and no list of characters closes that.
//
// Every range is written out as fixed code points, here and in
// termsafe.CheckLine, and none is read from Go's unicode tables, so the
// rule is the same under every Go release (FAILURE_PATTERNS 1003).
// termsafe.CheckLine refuses the invalid UTF-8, the control codes and the
// direction controls; hidden adds the rest.
func shownAsItself(name string) error {
	if err := termsafe.CheckLine(name); err != nil {
		return err
	}
	for i, r := range name {
		if why := hidden(r); why != "" {
			return fmt.Errorf("character %U at byte %d, %s", r, i, why)
		}
	}
	return nil
}

// hidden says why r keeps a name from showing as itself, for the
// characters shownAsItself refuses beyond termsafe.CheckLine, or returns
// "" for any other r.
func hidden(r rune) string {
	switch {
	case r == 0x2028, r == 0x2029:
		return "a line or paragraph separator, which starts a new line on a page"
	case r == 0x00AD, r == 0x200B, r == 0x2060, r == 0xFEFF:
		return "which shows as nothing, so the name would look like the same name without it"
	case r >= 0xE0000 && r <= 0xE007F:
		return "a tag character, which shows as nothing but spells out text a program reading the name sees"
	}
	return ""
}

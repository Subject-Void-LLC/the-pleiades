// Package recordname: which characters keep a name from showing as itself,
// read from Go's unicode tables when a name is chosen and never when one is
// read back.
package recordname

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// ShowsAsItself returns an error naming the first byte or character that
// would keep s from showing as itself on one line, or nil. The error names a
// character by its code point and byte offset and never quotes s. Check asks
// it of a record's name, and internal/facts of a new fact's key.
//
// What it refuses, and why:
//
//   - Invalid UTF-8. A reader shows a byte that is not UTF-8 as U+FFFD, so
//     two different names would print, and could be matched, as one
//     (FAILURE_PATTERNS 932). The replacement character written as itself
//     shows as itself, and is kept.
//   - Every control code (unicode.Cc): C0, DEL and C1. A terminal or a log
//     acts on these instead of showing them: a newline starts a line that
//     looks like the program's own.
//   - Every format character (unicode.Cf): the direction controls, which
//     change the order a name is displayed in ("Trojan Source"); the zero
//     width space, joiner and non-joiner, the soft hyphen, the word joiner
//     and the byte order mark; the invisible math operators; the tag
//     characters, which spell out ASCII text a program or a model sees and
//     a person does not; and the annotation, shorthand and musical format
//     controls. Each shows as nothing, or changes how the text around it
//     shows, so the name looks like another name and never matches what an
//     operator types.
//   - The default-ignorable code points Unicode files as letters or marks
//     (unicode.Other_Default_Ignorable_Code_Point): the Hangul fillers, the
//     combining grapheme joiner and the Khmer inherent vowels. A font draws
//     them as nothing, though they are not format characters.
//   - The variation selectors (unicode.Variation_Selector). They show as
//     nothing and change only how the character before them is drawn.
//   - Every space but U+0020 (unicode.Zs), and the line and paragraph
//     separators (unicode.Zl, unicode.Zp). A no-break or ideographic space
//     looks like a space and never matches one an operator types, and a
//     browser breaks a line at each separator.
//   - U+2800, the blank braille pattern, which Unicode files as a symbol
//     and every font draws as empty space.
//   - Surrogates, private-use characters and noncharacters (unicode.Cs, Co,
//     Noncharacter_Code_Point): a private-use character shows as whatever a
//     font chooses, or as nothing, and the other two are never shown.
//   - A name of spaces alone, which shows as nothing at all.
//
// It keeps U+0020, letters with accents, and every other letter, mark,
// digit, punctuation mark and symbol. It does not try to stop two names that
// look alike: a Latin "a" and a Cyrillic one, or a name and the same name
// with a space after it, look the same, and no list of characters closes
// that.
//
// The tables are the ones of the Go release that built the binary, so a
// later Unicode version can refuse a character an earlier one allowed. That
// is safe only because nothing reading a stored record asks this: a name or
// a key is held to it when it is chosen, never when it is read, listed,
// changed or verified (LESSONS 415, FAILURE_PATTERNS 1003 and 1031), so a Go
// upgrade can never make a stored record unreachable. termsafe.CheckLine,
// which is written out as fixed code points, refuses the invalid UTF-8, the
// control codes and the direction controls first, with its own messages.
func ShowsAsItself(s string) error {
	if err := termsafe.CheckLine(s); err != nil {
		return err
	}
	for i, r := range s {
		if why := hidden(r); why != "" {
			return fmt.Errorf("character %U at byte %d, %s", r, i, why)
		}
	}
	if s != "" && strings.Trim(s, " ") == "" {
		return fmt.Errorf("it is spaces alone, which show as nothing")
	}
	return nil
}

// hidden says why r keeps a name from showing as itself, for the
// characters ShowsAsItself refuses beyond termsafe.CheckLine, or returns ""
// for any other r. Each case reads a Go unicode table but the two fixed
// code points, so the order only picks the message.
func hidden(r rune) string {
	switch {
	case unicode.In(r, unicode.Zl, unicode.Zp):
		return "a line or paragraph separator, which starts a new line on a page"
	case r >= 0xE0000 && r <= 0xE007F:
		return "a tag character, which shows as nothing but spells out text a program reading the name sees"
	case r == 0x2800:
		return "the blank braille pattern, which shows as empty space"
	case r != ' ' && unicode.Is(unicode.Zs, r):
		return "a space other than U+0020, which looks like a space but never matches one an operator types"
	case unicode.Is(unicode.Variation_Selector, r):
		return "a variation selector, which shows as nothing and changes only how the character before it is drawn"
	case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point):
		return "which shows as nothing, so the name would look like another name without it"
	case unicode.Is(unicode.Co, r):
		return "a private-use character, which shows as whatever a font chooses, or as nothing"
	case unicode.In(r, unicode.Cs, unicode.Noncharacter_Code_Point):
		return "a code point that is never shown"
	}
	return ""
}

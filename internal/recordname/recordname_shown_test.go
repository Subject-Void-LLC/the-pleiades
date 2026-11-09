// Package recordname_test holds Check to a name that would not show as
// itself: each refused range at both of its edges, the code point beside
// each edge that stays usable, the names from real scripts that stay usable,
// and the ones that hold a joiner or a variation selector, which the rule
// now refuses (LESSONS 114: the near misses are the rows that matter).
//
// Every character outside printable ASCII is built from its code point in
// code, never written into this file, so the file holds no invisible
// character for internal/archtest to refuse and no tool can turn an escape
// into the real thing on the way in (FAILURE_PATTERNS 256, 834).
package recordname_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// cp is the one character at code point r, as a string.
func cp(r rune) string { return string(r) }

// TestCheck_RefusesACharacterThatWouldNotShowAsItself tables the rule one
// code point at a time, each inside "sw" and "1" so the name around it is
// otherwise usable. Refused: every control code (C0, DEL, C1), every format
// character (the direction controls, the joiners, the invisible operators,
// the tag characters), the default-ignorable letters and marks, the
// variation selectors, every space but U+0020, the line and paragraph
// separators, the blank braille pattern, and the private-use characters
// and noncharacters. Kept: the space, the replacement character itself, an
// unassigned code point, and the neighbor of every refused edge.
func TestCheck_RefusesACharacterThatWouldNotShowAsItself(t *testing.T) {
	for _, tc := range []struct {
		r  rune
		ok bool
	}{
		// C0 control codes, U+0000 to U+001F, with the ones a log or a
		// terminal acts on named among them.
		{0x0000, false}, {'\t', false}, {'\n', false}, {'\r', false}, {0x001B, false}, {0x001F, false},
		{' ', true}, {'~', true},
		// DEL and the C1 control codes, U+007F to U+009F; U+0085 is NEL, a
		// newline to some readers.
		{0x007F, false}, {0x0080, false}, {0x0085, false}, {0x009F, false},
		{0x00A0, false}, // a no-break space looks like a space and matches none
		{0x00AD, false},
		{0x00AE, true},
		{0x061B, true}, {0x061C, false}, {0x061D, true},
		{0x0600, false}, {0x05FF, true}, // U+05FF is unassigned
		{0x034E, true}, {0x034F, false}, {0x0350, true}, // the combining grapheme joiner
		// The Hangul fillers, letters Unicode marks default-ignorable.
		{0x115E, true}, {0x115F, false}, {0x1160, false}, {0x1161, true},
		{0x3163, true}, {0x3164, false}, {0x3165, true},
		{0xFF9F, true}, {0xFFA0, false}, {0xFFA1, true},
		// The Khmer inherent vowels, marks Unicode marks default-ignorable.
		{0x17B3, true}, {0x17B4, false}, {0x17B5, false}, {0x17B6, true},
		// The Mongolian variation selectors and vowel separator.
		{0x180A, true}, {0x180B, false}, {0x180E, false}, {0x180F, false}, {0x1810, true},
		{0x1680, false}, // the Ogham space mark
		// U+2000 to U+200F: the spaces, the zero width space, the two
		// joiners and the two direction marks, all refused.
		{0x1FFF, true}, {0x2000, false}, {0x200A, false}, {0x200B, false}, {0x200C, false}, {0x200D, false}, {0x200E, false}, {0x200F, false},
		{0x2010, true},
		// Line and paragraph separators, then the direction embeddings and
		// overrides.
		{0x2027, true}, {0x2028, false}, {0x2029, false}, {0x202A, false}, {0x202D, false}, {0x202E, false},
		{0x202F, false},
		{0x205E, true}, {0x205F, false}, {0x2060, false},
		// The invisible operators, U+2061 to U+2064, and U+2065, unassigned
		// but default-ignorable.
		{0x2061, false}, {0x2064, false}, {0x2065, false},
		// The direction isolates, U+2066 to U+2069, then the deprecated
		// format controls to U+206F.
		{0x2066, false}, {0x2069, false}, {0x206A, false}, {0x206F, false}, {0x2070, true},
		{0x27FF, true}, {0x2800, false}, {0x2801, true}, // the blank braille pattern
		{0x2FFF, true}, {0x3000, false}, {0x3001, true}, // the ideographic space
		// Private use, U+E000 to U+F8FF.
		{0xE000, false}, {0xF8FF, false}, {0xF900, true},
		// The noncharacters U+FDD0 to U+FDEF.
		{0xFDCF, true}, {0xFDD0, false}, {0xFDEF, false}, {0xFDF0, true},
		{0xFDFF, true}, {0xFE00, false}, {0xFE0F, false}, {0xFE10, true}, // the variation selectors
		{0xFEFE, true}, {0xFEFF, false}, {0xFF00, true},
		// The interlinear annotation controls, then the replacement character,
		// when it is really there, and the two noncharacters ending the plane.
		{0xFFF8, false}, {0xFFF9, false}, {0xFFFB, false}, {0xFFFC, true},
		{0xFFFD, true}, {0xFFFE, false}, {0xFFFF, false},
		// The shorthand and the musical format controls.
		{0x1BC9F, true}, {0x1BCA0, false}, {0x1BCA3, false}, {0x1BCA4, true},
		{0x1D172, true}, {0x1D173, false}, {0x1D17A, false}, {0x1D17B, true},
		// The tag characters, U+E0000 to U+E007F.
		{0xDFFFD, true}, {0xE0000, false}, {0xE0001, false}, {0xE0041, false}, {0xE007F, false},
		{0xE0100, false}, {0xE01EF, false}, {0xE01F0, false}, // ideographic variation selectors, then default-ignorable
		// The private-use planes, and the last code point, a noncharacter.
		{0xEFFFD, true}, {0xF0000, false}, {0x10FFFD, false}, {0x10FFFF, false},
	} {
		name := "sw" + cp(tc.r) + "1"
		t.Run(fmt.Sprintf("%U", tc.r), func(t *testing.T) {
			err := recordname.Check(name)
			if tc.ok {
				if err != nil {
					t.Fatalf("Check(%q) = %v, want nil", name, err)
				}
				return
			}
			if !errors.Is(err, recordname.ErrUnusable) {
				t.Fatalf("Check(%q) = %v, want an error matching ErrUnusable", name, err)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%U at byte 2", tc.r)) {
				t.Errorf("Check(%q) says %q, want it to name %U at byte 2", name, err, tc.r)
			}
			// The name is never what the message is made of: neither the
			// character nor the name around it comes back.
			if strings.Contains(err.Error(), cp(tc.r)) || strings.Contains(err.Error(), name) {
				t.Errorf("Check(%q) echoes the name or its character: %q", name, err)
			}
			// A name stays readable as an address: Addressable is the rule
			// a reader asks, and it does not grow.
			if !recordname.Addressable(name) {
				t.Errorf("Addressable(%q) = false; a stored name holding %U must stay linkable", name, tc.r)
			}
		})
	}
}

// TestCheck_RefusesWhatIsNotUTF8 refuses a byte that is not UTF-8, alone,
// at the start and as a cut-off sequence, and names where it is. The
// replacement character U+FFFD written as itself stays usable (above): a
// reader that decodes a bad byte as U+FFFD would otherwise take the two as
// one name (FAILURE_PATTERNS 932).
func TestCheck_RefusesWhatIsNotUTF8(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   int
	}{
		{"sw\xff1", 2},
		{"\xc0", 0},
		{"sw\xe2\x80", 2},
	} {
		err := recordname.Check(tc.name)
		if !errors.Is(err, recordname.ErrUnusable) {
			t.Fatalf("Check(%q) = %v, want an error matching ErrUnusable", tc.name, err)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("invalid UTF-8 at byte %d", tc.at)) {
			t.Errorf("Check(%q) says %q, want it to name the byte", tc.name, err)
		}
	}
}

// TestCheck_KeepsNamesFromRealScripts: the rule takes nothing most real
// names need. Each row is a name a sync source could legitimately send (a
// VM's display name, a tag): an accented letter, a space, spaces around a
// name, Devanagari's conjunct with no joiner, Arabic and Hebrew, which order
// themselves with no direction control, Chinese, and an emoji drawn in its
// default form.
func TestCheck_KeepsNamesFromRealScripts(t *testing.T) {
	for _, name := range []string{
		"caf" + cp(0xE9) + "-01",
		"Build Server 1",
		" leading and trailing ",
		cp(0x0915) + cp(0x094D) + cp(0x0937),
		cp(0x062E) + cp(0x0627) + cp(0x062F) + cp(0x0645) + "-1",
		cp(0x05E9) + cp(0x05E8) + cp(0x05EA),
		cp(0x670D) + cp(0x52A1) + cp(0x5668),
		"love" + cp(0x2764),
	} {
		if err := recordname.Check(name); err != nil {
			t.Errorf("Check(%q) = %v, want nil", name, err)
		}
	}
}

// TestCheck_RefusesTheJoinersAndSelectorsRealNamesCanHold pins what the
// rule costs: a name from a real script that holds a joiner or a variation
// selector is refused, because each is a format character or a variation
// selector, which shows as nothing. Persian written with the zero width
// non-joiner its spelling uses, Devanagari with a zero width joiner, an
// emoji with its color selector, and a family emoji built with joiners. The
// error names the first such character.
func TestCheck_RefusesTheJoinersAndSelectorsRealNamesCanHold(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   string
	}{
		{cp(0x0645) + cp(0x06CC) + cp(0x200C) + cp(0x062E) + cp(0x0648) + cp(0x0627) + cp(0x0647) + cp(0x0645), "U+200C at byte 4"},
		{cp(0x0915) + cp(0x094D) + cp(0x200D) + cp(0x0937), "U+200D at byte 6"},
		{"love" + cp(0x2764) + cp(0xFE0F), "U+FE0F at byte 7"},
		{cp(0x1F468) + cp(0x200D) + cp(0x1F469) + cp(0x200D) + cp(0x1F467), "U+200D at byte 4"},
	} {
		err := recordname.Check(tc.name)
		if !errors.Is(err, recordname.ErrUnusable) {
			t.Errorf("Check(%q) = %v, want an error matching ErrUnusable", tc.name, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.at) {
			t.Errorf("Check(%q) says %q, want it to name %s", tc.name, err, tc.at)
		}
	}
}

// TestCheck_RefusesSpacesAlone: a name of U+0020 alone shows as nothing,
// though each space is allowed inside a name.
func TestCheck_RefusesSpacesAlone(t *testing.T) {
	for _, name := range []string{" ", "   "} {
		err := recordname.Check(name)
		if !errors.Is(err, recordname.ErrUnusable) {
			t.Errorf("Check(%q) = %v, want an error matching ErrUnusable", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "spaces alone") {
			t.Errorf("Check(%q) says %q, want it to say the name is spaces alone", name, err)
		}
	}
	if err := recordname.Check(" a "); err != nil {
		t.Errorf("Check(%q) = %v, want nil: spaces around a name are allowed", " a ", err)
	}
}

// TestCheck_RefusesANameThatWouldReadAsTwoLines refuses the name a sync
// source could use to print a line of its own under any message that names
// the device, and says why without repeating it.
func TestCheck_RefusesANameThatWouldReadAsTwoLines(t *testing.T) {
	name := "sw2\npleiades: approved"
	err := recordname.Check(name)
	if !errors.Is(err, recordname.ErrUnusable) {
		t.Fatalf("Check(%q) = %v, want an error matching ErrUnusable", name, err)
	}
	msg := err.Error()
	for _, want := range []string{"the name cannot be used", "U+000A at byte 3", "one line"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Check(%q) says %q, want it to say %q", name, msg, want)
		}
	}
	if strings.Contains(msg, "approved") || strings.Contains(msg, "\n") {
		t.Errorf("Check(%q) repeats the name: %q", name, msg)
	}
}

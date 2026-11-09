// Package recordname_test holds Check to a name that would not show as
// itself: each refused range at both of its edges, the code point beside
// each edge that stays usable, and the names from real scripts that hold a
// joiner, a variation selector or an accent (LESSONS 114: the near misses
// are the rows that matter).
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
// otherwise usable. Refused: every control code (C0, DEL, C1), the line and
// paragraph separators, the direction controls, the four invisible
// characters a name picks up by accident and the tag characters. Kept:
// the space, the joiners scripts need, the variation selectors, the
// replacement character itself, and the neighbor of every refused edge.
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
		{0x00A0, true}, // a no-break space shows as a space
		{0x00AD, false},
		{0x00AE, true},
		{0x061B, true}, {0x061C, false}, {0x061D, true},
		// U+200B to U+200F: the zero width space is refused, the two
		// joiners kept, the two direction marks refused.
		{0x200A, true}, {0x200B, false}, {0x200C, true}, {0x200D, true}, {0x200E, false}, {0x200F, false},
		{0x2010, true},
		// Line and paragraph separators, then the direction embeddings and
		// overrides.
		{0x2027, true}, {0x2028, false}, {0x2029, false}, {0x202A, false}, {0x202D, false}, {0x202E, false},
		{0x202F, true},
		{0x205F, true}, {0x2060, false},
		// The direction isolates, U+2066 to U+2069.
		{0x2066, false}, {0x2069, false},
		{0xFE0F, true}, // variation selector 16: an emoji's color form
		{0xFEFE, true}, {0xFEFF, false}, {0xFF00, true},
		{0xFFFD, true}, // the replacement character, when it is really there
		// The tag characters, U+E0000 to U+E007F.
		{0xDFFFF, true}, {0xE0000, false}, {0xE0001, false}, {0xE0041, false}, {0xE007F, false},
		{0xE0100, true}, // an ideographic variation selector
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

// TestCheck_KeepsNamesFromRealScripts: the rule takes nothing a real name
// needs. Each row is a name a sync source could legitimately send (a VM's
// display name, a tag): an accented letter, a space, Persian written with
// the zero width non-joiner its spelling needs, Devanagari with a zero
// width joiner, Arabic and Hebrew, which order themselves with no
// direction control, Chinese, an emoji with its color selector and a
// family emoji built with joiners.
func TestCheck_KeepsNamesFromRealScripts(t *testing.T) {
	for _, name := range []string{
		"caf" + cp(0xE9) + "-01",
		"Build Server 1",
		" leading and trailing ",
		cp(0x0645) + cp(0x06CC) + cp(0x200C) + cp(0x062E) + cp(0x0648) + cp(0x0627) + cp(0x0647) + cp(0x0645),
		cp(0x0915) + cp(0x094D) + cp(0x200D) + cp(0x0937),
		cp(0x062E) + cp(0x0627) + cp(0x062F) + cp(0x0645) + "-1",
		cp(0x05E9) + cp(0x05E8) + cp(0x05EA),
		cp(0x670D) + cp(0x52A1) + cp(0x5668),
		"love" + cp(0x2764) + cp(0xFE0F),
		cp(0x1F468) + cp(0x200D) + cp(0x1F469) + cp(0x200D) + cp(0x1F467),
	} {
		if err := recordname.Check(name); err != nil {
			t.Errorf("Check(%q) = %v, want nil", name, err)
		}
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

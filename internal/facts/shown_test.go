// Package facts_test: ShownKey shows a stored key a new fact could not take
// with each character that would not show as itself escaped.
package facts_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// backslash is U+005C, written as a code point so no escape in this file's
// source reads as one ShownKey writes.
var backslash = string(rune(0x5C))

// esc is the escape ShownKey writes for a code point, built in code.
func esc(hex string) string { return backslash + "u{" + hex + "}" }

// TestShownKey_EscapesWhatWouldNotShowAsItself tables the rule: a key a new
// fact could take shows as it is; one holding a zero width character, a soft
// hyphen, a tag character, a line or paragraph separator, a no-break space,
// a private-use character, an unassigned code point, a newline or a byte
// that is not UTF-8 shows each escaped; a backslash in such a key is escaped
// too, and "." shows as itself.
func TestShownKey_EscapesWhatWouldNotShowAsItself(t *testing.T) {
	r := firstUnassigned(t)
	for in, want := range map[string]string{
		"os.family":                          "os.family",
		".":                                  ".",
		"pkg.a" + string(rune(0x200B)) + "b": "pkg.a" + esc("200B") + "b",
		"pkg.a" + string(rune(0x00AD)) + "b": "pkg.a" + esc("AD") + "b",
		"pkg" + string(rune(0xE0041)):        "pkg" + esc("E0041"),
		"pkg" + string(rune(0x2028)) + "x":   "pkg" + esc("2028") + "x",
		"pkg" + string(rune(0x2029)):         "pkg" + esc("2029"),
		"pkg" + string(rune(0x00A0)):         "pkg" + esc("A0"),
		"pkg" + string(rune(0xE000)):         "pkg" + esc("E000"),
		"pkg" + string(r):                    "pkg" + esc(strings.ToUpper(strings.TrimLeft(hexOf(r), "0"))),
		"vol\nkey":                           "vol" + esc("A") + "key",
		"a\xffb":                             "a" + backslash + "x{FF}b",
		"a\\b" + string(rune(0x200B)):        "a" + esc("5C") + "b" + esc("200B"),
		"caf" + string(rune(0xE9)):           "caf" + string(rune(0xE9)),
	} {
		if got := facts.ShownKey(in); got != want {
			t.Errorf("ShownKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// hexOf is r's code point in hex, as %X writes it.
func hexOf(r rune) string {
	const digits = "0123456789ABCDEF"
	out := ""
	for v := int(r); v > 0; v /= 16 {
		out = string(digits[v%16]) + out
	}
	return out
}

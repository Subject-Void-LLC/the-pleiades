// Tests of Quote (quote.go): a plain name as it is, anything else quoted
// with every byte outside printable ASCII escaped, and nothing past
// MaxQuoted bytes.
package termsafe_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// TestQuote_LeavesAPlainNameAndQuotesTheRest tables the rule.
func TestQuote_LeavesAPlainNameAndQuotesTheRest(t *testing.T) {
	override := string(rune(0x202E))
	for in, want := range map[string]string{
		"facts.gather":              "facts.gather",
		"tasks[0].block[1].undo[0]": "tasks[0].block[1].undo[0]",
		"":                          `""`,
		"two words":                 `"two words"`,
		`a"quote`:                   `"a\"quote"`,
		"line\nbreak":               `"line\nbreak"`,
		"fi" + override + "le":      `"fi\u202ele"`,
		"caf" + string(rune(0xE9)):  `"caf\u00e9"`,
		"a\xffb":                    `"a\xffb"`,
		strings.Repeat("a", 128):    strings.Repeat("a", 128),
	} {
		if got := termsafe.Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestQuote_BoundsAMegabyte: a megabyte holding a direction override comes
// back as at most MaxQuoted bytes of it, escaped, with its length, and a cut
// never splits a character.
func TestQuote_BoundsAMegabyte(t *testing.T) {
	huge := strings.Repeat("zq9"+string(rune(0x202E)), 1<<20/6)
	got := termsafe.Quote(huge)
	if len(got) > 4*termsafe.MaxQuoted+64 || strings.ContainsRune(got, 0x202E) {
		t.Fatalf("Quote of %d bytes gave %d bytes: %.200s", len(huge), len(got), got)
	}
	if !strings.HasSuffix(got, "(cut from "+strconv.Itoa(len(huge))+" bytes)") {
		t.Errorf("the cut does not say the whole length: %.200s", got)
	}
	// 127 a's then a two-byte character straddling the bound.
	straddle := strings.Repeat("a", 127) + string(rune(0xE9)) + "tail"
	if got := termsafe.Quote(straddle); strings.Contains(got, `\x`) || !strings.HasPrefix(got, `"`+strings.Repeat("a", 127)+`"...`) {
		t.Errorf("a cut through a character: %s", got)
	}
}

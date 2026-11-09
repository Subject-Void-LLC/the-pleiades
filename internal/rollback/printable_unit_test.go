// Tests of Printable's own rule (printable.go).
package rollback_test

import (
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// TestPrintable_QuotesOnlyWhatIsNotAPlainName: a registered method name or
// a graph node id prints as it is; anything else prints quoted.
func TestPrintable_QuotesOnlyWhatIsNotAPlainName(t *testing.T) {
	override := string(rune(0x202e))
	for in, want := range map[string]string{
		"file.copy":                             "file.copy",
		"tasks[0].block[1].undo[0]":             "tasks[0].block[1].undo[0]",
		"<unregistered>":                        "<unregistered>",
		"":                                      `""`,
		"fi" + override + "le.copy":             strconv.Quote("fi" + override + "le.copy"),
		"tasks[0] and more":                     `"tasks[0] and more"`,
		"line\nbreak":                           `"line\nbreak"`,
		"zero" + string(rune(0x200b)) + "width": strconv.Quote("zero" + string(rune(0x200b)) + "width"),
	} {
		if got := rollback.Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}

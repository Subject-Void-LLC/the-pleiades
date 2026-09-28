// Package termsafe: tests of the terminal-safety checks and escapes.
package termsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestEscape covers what each function escapes and what it keeps.
func TestEscape(t *testing.T) {
	for _, tc := range []struct {
		in, escaped, line string
	}{
		{in: "plain text", escaped: "plain text", line: "plain text"},
		{in: "two\nlines\tand a tab", escaped: "two\nlines\tand a tab", line: `two\nlines\tand a tab`},
		{in: "clear\x1b[2Jscreen", escaped: `clear\x1b[2Jscreen`, line: `clear\x1b[2Jscreen`},
		{in: "over\rwrite", escaped: `over\rwrite`, line: `over\rwrite`},
		{in: "windows\r\nline", escaped: "windows\nline", line: `windows\r\nline`},
		{in: "bell\x07 del\x7f c1\u009b", escaped: `bell\x07 del\x7f c1\x9b`, line: `bell\x07 del\x7f c1\x9b`},
		{in: "abc\u202edcba", escaped: `abc\u202edcba`, line: `abc\u202edcba`},
		{in: "iso\u2066late\u2069", escaped: `iso\u2066late\u2069`, line: `iso\u2066late\u2069`},
		{in: "bad\xffbyte", escaped: `bad\xffbyte`, line: `bad\xffbyte`},
		{in: "ünïcødé ok", escaped: "ünïcødé ok", line: "ünïcødé ok"},
	} {
		if got := Escape(tc.in); got != tc.escaped {
			t.Errorf("Escape(%q) = %q, want %q", tc.in, got, tc.escaped)
		}
		if got := EscapeLine(tc.in); got != tc.line {
			t.Errorf("EscapeLine(%q) = %q, want %q", tc.in, got, tc.line)
		}
	}
}

// TestCheck covers the refusal form.
func TestCheck(t *testing.T) {
	for in, ok := range map[string]bool{
		"a summary":           true,
		"two\nparagraphs\tok": true,
		"hidden\x1b[8m text":  false,
		"reversed\u202e":      false,
		"carriage\rreturn":    false,
		"invalid \xff utf-8":  false,
	} {
		if err := Check(in); (err == nil) != ok {
			t.Errorf("Check(%q) = %v, want ok=%v", in, err, ok)
		}
	}
}

// FuzzEscape proves, for any input, that Escape leaves nothing a terminal
// would act on and that EscapeLine leaves one line.
func FuzzEscape(f *testing.F) {
	for _, s := range []string{"", "a\x1b[31mb", "x\r\ny", "\u202e", "\xff\xfe", "\n\t"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Escape(s)
		if !utf8.ValidString(out) {
			t.Fatalf("Escape(%q) is not valid UTF-8: %q", s, out)
		}
		if err := Check(out); err != nil {
			t.Fatalf("Escape(%q) = %q still has %v", s, out, err)
		}
		if line := EscapeLine(s); strings.ContainsAny(line, "\n\t") || Check(line) != nil {
			t.Fatalf("EscapeLine(%q) = %q is not one safe line", s, line)
		}
	})
}

// TestCheckLine covers the one-line refusal: everything Check refuses,
// plus newline and tab.
func TestCheckLine(t *testing.T) {
	for in, ok := range map[string]bool{
		"a@example.test":                  true,
		"a name with spaces":              true,
		"two\nlines":                      false,
		"a\ttab":                          false,
		"hidden\x1b[8m text":              false,
		"nul\x00byte":                     false,
		"del\x7f":                         false,
		"c1 " + string(rune(0x85)):        false,
		"reversed" + string(rune(0x202e)): false,
		"invalid \xff utf-8":              false,
		"accented " + string(rune(0xe9)):  true,
		"a real " + string(rune(0xfffd)) + " replacement character": true,
	} {
		if err := CheckLine(in); (err == nil) != ok {
			t.Errorf("CheckLine(%q) = %v, want ok=%v", in, err, ok)
		}
	}
}

// FuzzCheckLine proves CheckLine and EscapeLine draw the same line: a
// string passes CheckLine exactly when EscapeLine would leave it as it is.
// A caller that refuses with one and another that escapes with the other
// therefore never disagree about what is unsafe.
func FuzzCheckLine(f *testing.F) {
	for _, s := range []string{"", "a@b", "a\nb", "\t", "\x1b", "\xff", "\xef\xbf\xbd", "\\x1b literal"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		refused := CheckLine(s) != nil
		escaped := EscapeLine(s) != s
		if refused != escaped {
			t.Fatalf("CheckLine(%q) refused=%v but EscapeLine changed it=%v", s, refused, escaped)
		}
	})
}

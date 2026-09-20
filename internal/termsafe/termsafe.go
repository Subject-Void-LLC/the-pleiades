// Package termsafe keeps text that came from somewhere untrusted (an
// external Collection program, a device's output) from controlling the
// terminal it is printed on.
//
// A terminal acts on some characters instead of showing them: an escape
// sequence can clear the screen, move the cursor or recolor what follows,
// a carriage return can overwrite the line already printed, and a Unicode
// bidirectional override can make text read in a different order from
// the order it is stored in. Printed raw, any of these lets whoever wrote
// the text make the output say something else, such as a fake "approved"
// line under the prompt that decides whether a program may run. So this
// package escapes them visibly (\x1b, \r, \u202e) instead of dropping
// them: the reader sees that something was there.
package termsafe

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Unsafe reports whether r is a character a terminal may act on rather
// than show: every C0 control character except tab and newline, DEL,
// every C1 control character, and the Unicode characters that change the
// direction text is displayed in (the ones "Trojan Source" relies on).
// Newline is safe here: Escape keeps it, and EscapeLine escapes it.
func Unsafe(r rune) bool {
	switch {
	case r == '\t' || r == '\n':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}

// Escape returns s with every Unsafe character written as a visible
// escape, keeping tab and newline, so multi-line output (a command's
// stdout) still reads as lines. A Windows line ending ("\r\n") is read as
// a plain newline rather than shown as "\r" at the end of every line.
// Invalid UTF-8 is shown byte by byte as \xNN.
func Escape(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return escape(s, false)
}

// EscapeLine is Escape for text that must stay on one line (a name, an
// error message quoted into a sentence): newlines and tabs are escaped
// too, so the text cannot start a line of output that looks like the
// program's own.
func EscapeLine(s string) string {
	return escape(s, true)
}

// Check returns an error naming the first Unsafe character in s, or nil.
// A caller that can refuse the text outright, rather than escape it, uses
// this.
func Check(s string) error {
	for i, r := range s {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(s[i:]); size == 1 {
				return fmt.Errorf("invalid UTF-8 at byte %d", i)
			}
		}
		if Unsafe(r) {
			return fmt.Errorf("character %U at byte %d, which a terminal acts on instead of showing", r, i)
		}
	}
	return nil
}

// escape writes s with every Unsafe character, and newline and tab too
// when oneLine is set, as a visible escape.
func escape(s string, oneLine bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case oneLine && r == '\n':
			b.WriteString(`\n`)
		case oneLine && r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == 0x1b:
			b.WriteString(`\x1b`)
		case Unsafe(r) && r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case Unsafe(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

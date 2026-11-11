// Package termsafe: Quote, for text another process delivered that a
// refusal, a warning or a log line names.
package termsafe

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// MaxQuoted bounds how many bytes of a text Quote shows.
const MaxQuoted = 128

// Quote returns s as one token a message or a log line can carry, whatever
// s holds and however long it is. A plain name (1 to MaxQuoted bytes of
// visible ASCII other than a quote or a backslash, which every method name,
// node id and digest is) is returned as it is. Anything else is quoted with
// every byte outside printable ASCII escaped (strconv.QuoteToASCII, which
// reads no Unicode table for its answer), cut to at most MaxQuoted bytes of
// s at a character boundary, with the whole length after the quote when it
// was cut: `"abc..." (cut from 1048576 bytes)`. So a direction override, an
// invisible character or a line break never reaches the reader raw, and a
// megabyte of it never reaches a log line.
func Quote(s string) string {
	if plainName(s) {
		return s
	}
	return QuoteBounded(s)
}

// QuoteBounded is Quote without the plain-name case: s always quoted, every
// byte outside printable ASCII escaped, cut to at most MaxQuoted bytes with
// its whole length after the quote when it was longer. A message that
// quotes every name of a kind (a Collection group, a version, a digest), so
// a reader can tell where one ends, uses it in place of %q.
func QuoteBounded(s string) string {
	if len(s) <= MaxQuoted {
		return strconv.QuoteToASCII(s)
	}
	cut := MaxQuoted
	// Back up to the start of a character, so the cut does not show as an
	// invalid byte that s never held there.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s... (cut from %d bytes)", strconv.QuoteToASCII(s[:cut]), len(s))
}

// plainName reports whether s is 1 to MaxQuoted bytes of visible ASCII with
// no quote or backslash, which Quote returns unquoted.
func plainName(s string) bool {
	if s == "" || len(s) > MaxQuoted {
		return false
	}
	for i := 0; i < len(s); i++ {
		if b := s[i]; b < 0x21 || b > 0x7e || b == '"' || b == '\\' {
			return false
		}
	}
	return true
}

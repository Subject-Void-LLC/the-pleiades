// This file reads a source URL as any reader along the way could come to
// read it, so the credential rules in secret_url.go judge what a URL may
// become rather than only what it shows.
//
// They used to compare against lists of shapes: "%40" and two lookalikes for
// "@", then "%3F", "%23" and four lookalikes for "?" and "#". Each review
// found a shape the lists missed (U+FE16 and U+2047 to U+2049 also become "?"
// under NFKC; "%253F" decodes to "%3F" and then to "?"; a fullwidth percent
// sign becomes "%"), and a missed shape is a signature stored in plain text
// and shown raw (LESSONS 190: a list fails open on every shape nobody listed).
// revealed canonicalizes instead, the order CodeGuard's input validation rule
// gives: decode and normalize first, then check.
package project

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// revealRounds bounds how many times revealed normalizes and decodes. Each
// round removes at least one layer of encoding, and no repository URL needs
// more than one, so text still changing after this many is refused rather
// than read further.
const revealRounds = 8

// revealed returns raw as a reader could come to read it: Unicode NFKC
// normalized (so a fullwidth or small form becomes the character it stands
// for) and percent-decoded, repeated until the text stops changing, and
// whether it stopped within revealRounds. A caller treats text that did not
// settle as carrying whatever it fears.
func revealed(raw string) (string, bool) {
	text := raw
	for range revealRounds {
		next := percentDecoded(norm.NFKC.String(text))
		if next == text {
			return text, true
		}
		text = next
	}
	return text, false
}

// percentDecoded decodes every "%XX" in text whose two characters are hex
// digits, in either case, and leaves every other "%" as it is. It is lenient
// on purpose: url.PathUnescape refuses the whole text on one bad escape,
// which would hide the good escapes beside it.
func percentDecoded(text string) string {
	if !strings.Contains(text, "%") {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+2 < len(text) {
			hi, okHi := hexValue(text[i+1])
			lo, okLo := hexValue(text[i+2])
			if okHi && okLo {
				out.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		out.WriteByte(text[i])
	}
	return out.String()
}

// hexValue is the value of one hex digit, and whether c is one.
func hexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

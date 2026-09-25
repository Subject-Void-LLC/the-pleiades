// PowerShell string quoting for values spliced into a script this
// platform builds.
//
// It lives here, beside Run, because every package that builds a
// PowerShell script for a Windows device (pkg/winrmsvc, pkg/winrmdism,
// pkg/vboxmanage) needs the identical rule, and a quoting rule copied
// into each of them is how one copy ends up wrong while the others are
// fixed. That is not hypothetical: the private copies this replaced were
// all wrong in the same way (FAILURE_PATTERNS 354).
package winrmexec

import "strings"

// singleQuoteChars is every character PowerShell's tokenizer accepts as a
// single quote: the ASCII apostrophe and four typographic quotes. The
// PowerShell language specification (section 2.3.5.2,
// single-quote-character) lists all five, and a single-quoted literal
// ends at ANY of them, not only at the ASCII one.
var singleQuoteChars = []string{
	"'",      // U+0027 APOSTROPHE
	"\u2018", // LEFT SINGLE QUOTATION MARK
	"\u2019", // RIGHT SINGLE QUOTATION MARK
	"\u201A", // SINGLE LOW-9 QUOTATION MARK
	"\u201B", // SINGLE HIGH-REVERSED-9 QUOTATION MARK
}

// QuotePS renders s as a PowerShell single-quoted string literal, safe to
// splice into a script.
//
// Inside single quotes PowerShell expands no variable and honors no
// backtick escape. The only way to leave the literal is a quote
// character, and a doubled quote character is how the language embeds
// one. So the complete escape is doubling every character PowerShell
// treats as a single quote, which is all five in singleQuoteChars, not
// only the ASCII apostrophe. Doubling only the apostrophe let a value
// containing U+2019 close the literal early and run the rest of itself
// as PowerShell; that was measured against Windows PowerShell 5.1 with
// each of the four typographic quotes. This matches what PowerShell's
// own CodeGeneration.EscapeSingleQuotedStringContent does.
//
// A doubled typographic quote stays a typographic quote in the value
// PowerShell sees, so a name that legitimately contains one survives
// the round trip unchanged.
func QuotePS(s string) string {
	for _, q := range singleQuoteChars {
		// Doubling each quote character embeds it literally.
		s = strings.ReplaceAll(s, q, q+q)
	}
	return "'" + s + "'"
}

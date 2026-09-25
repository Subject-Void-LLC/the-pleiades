// Tests for QuotePS: the escape every PowerShell script this platform
// builds relies on.
//
// The injection these guard against was measured against a real Windows
// PowerShell 5.1: a value containing any of the four typographic single
// quotes closed a literal quoted by the old rule and ran the rest of the
// value as a command. PowerShell is not available to a Go test, so the
// tests below check the property that made the measured fix work (every
// single-quote character appears doubled, so none can end the literal)
// with a Go model of PowerShell's single-quoted string tokenizer, and the
// Windows Release Gates exercise the real parser end to end.
package winrmexec

import (
	"strings"
	"testing"
)

// typographic returns the four non-ASCII characters PowerShell treats as
// single quotes, built from code points so no editor or tool can turn
// them into something else in this file.
func typographic() []string {
	return []string{
		string(rune(0x2018)),
		string(rune(0x2019)),
		string(rune(0x201A)),
		string(rune(0x201B)),
	}
}

// isSingleQuote reports whether r is one of the five characters
// PowerShell's tokenizer ends a single-quoted literal at.
func isSingleQuote(r rune) bool {
	switch r {
	case '\'', 0x2018, 0x2019, 0x201A, 0x201B:
		return true
	}
	return false
}

// parsePSSingleQuoted models PowerShell's single-quoted string literal:
// it reads one literal from the start of src and returns the value it
// denotes and whatever source text follows it. A doubled quote character
// is one embedded quote; any single quote character not followed by
// another ends the literal. ok is false when src does not start with a
// quote or the literal never ends.
func parsePSSingleQuoted(src string) (value, rest string, ok bool) {
	runes := []rune(src)
	if len(runes) == 0 || !isSingleQuote(runes[0]) {
		return "", "", false
	}
	var b strings.Builder
	for i := 1; i < len(runes); i++ {
		if !isSingleQuote(runes[i]) {
			b.WriteRune(runes[i])
			continue
		}
		// A quote character followed by another is an embedded quote:
		// PowerShell keeps the first and skips the second.
		if i+1 < len(runes) && isSingleQuote(runes[i+1]) {
			b.WriteRune(runes[i])
			i++
			continue
		}
		// A lone quote character ends the literal here.
		return b.String(), string(runes[i+1:]), true
	}
	return "", "", false
}

func TestQuotePS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain name", input: "nginx", want: "'nginx'"},
		{name: "embedded apostrophe is doubled", input: "O'Brien", want: "'O''Brien'"},
		{name: "empty string", input: "", want: "''"},
		{name: "semicolon is inert inside single quotes", input: `svc; Remove-Item C:\`, want: `'svc; Remove-Item C:\'`},
		{name: "dollar sign is not expanded", input: "$env:TEMP", want: "'$env:TEMP'"},
		{name: "only apostrophes", input: "'''", want: "''''''''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuotePS(tt.input); got != tt.want {
				t.Errorf("QuotePS(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestQuotePS_TypographicQuotesAreDoubled pins the fix: each of the four
// typographic single quotes is doubled exactly as the apostrophe is. The
// old rule left these alone, which is what let one end the literal.
func TestQuotePS_TypographicQuotesAreDoubled(t *testing.T) {
	for _, q := range typographic() {
		input := "x" + q + "; Write-Output INJECTED; " + q + "y"
		want := "'x" + q + q + "; Write-Output INJECTED; " + q + q + "y'"
		if got := QuotePS(input); got != want {
			t.Errorf("QuotePS(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestQuotePS_TheMeasuredPayloadStaysOneLiteral replays the exact payload
// shape that injected on Windows PowerShell 5.1 through the tokenizer
// model: the quoted value must be ONE literal, denote the input exactly,
// and leave no source text behind it for PowerShell to run.
func TestQuotePS_TheMeasuredPayloadStaysOneLiteral(t *testing.T) {
	for _, q := range typographic() {
		input := "x" + q + "; Write-Output INJECTED; Get-Variable -Name " + q + "y"
		value, rest, ok := parsePSSingleQuoted(QuotePS(input))
		if !ok {
			t.Fatalf("QuotePS output for %q is not a closed literal", input)
		}
		if value != input {
			t.Errorf("literal denotes %q, want the input %q", value, input)
		}
		if rest != "" {
			t.Errorf("literal ended early; PowerShell would run %q", rest)
		}
	}
}

// TestParsePSSingleQuoted_ModelMatchesTheMeasuredBreakout is the control
// for the test above: fed the OLD rule's output for the measured payload,
// the model must find the same early close the real PowerShell did.
// Without this, a model that never ends a literal would pass everything.
func TestParsePSSingleQuoted_ModelMatchesTheMeasuredBreakout(t *testing.T) {
	oldQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	for _, q := range typographic() {
		input := "x" + q + "; Write-Output INJECTED; Get-Variable -Name " + q + "y"
		_, rest, ok := parsePSSingleQuoted(oldQuote(input))
		if !ok || !strings.Contains(rest, "Write-Output INJECTED") {
			t.Errorf("model did not reproduce the measured breakout for U+%04X: ok=%v rest=%q", []rune(q)[0], ok, rest)
		}
	}
}

// FuzzQuotePS checks, for arbitrary input, that the quoted form is one
// closed literal denoting exactly the input with nothing after it.
func FuzzQuotePS(f *testing.F) {
	f.Add("nginx")
	f.Add("O'Brien")
	f.Add("")
	for _, q := range typographic() {
		f.Add("a" + q + "; b; " + q)
	}
	f.Fuzz(func(t *testing.T, input string) {
		value, rest, ok := parsePSSingleQuoted(QuotePS(input))
		if !ok {
			t.Fatalf("QuotePS(%q) is not a closed literal", input)
		}
		if rest != "" {
			t.Fatalf("QuotePS(%q) ended early, leaving %q", input, rest)
		}
		// Invalid UTF-8 cannot round-trip through a rune model, and a
		// PowerShell script is UTF-16 by the time it runs, so only a
		// valid input is compared for exact equality.
		if strings.ToValidUTF8(input, "") == input && value != input {
			t.Fatalf("QuotePS(%q) denotes %q", input, value)
		}
	})
}

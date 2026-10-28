// Package farside turns what a remote system answered into text fit for an
// error: the far side's own words, found in its JSON error fields or its XML
// fault when it sent one, otherwise its whole answer on one line, bounded,
// and quoted so a control character is shown rather than acted on.
//
// It exists because a refusal's reason is the one thing an operator or an
// agent can act on, and clients kept cutting it off: an error made of a
// body's first line says "{" when a controller answers with indented JSON,
// and one made of a status code alone says nothing at all. Every client that
// puts a remote answer into an error calls this package, and
// internal/archtest refuses a new first-line helper.
//
// Masking. A pkg/ package may not import internal/redact, which holds the
// platform's shape and by-value masking rules, so this package masks nothing
// by itself. Every method's error is masked by internal/redact where it
// leaves the engine (the executor, the native adapter and the run report all
// pass it through redact.Text), so a secret-shaped string in a far side's
// answer is masked there. A caller holding a masking rule of its own, such
// as pkg/netcfg.Mask for configuration text, passes it to Masked, which
// applies it before the text is bounded, so a secret is never cut in half
// and half left showing.
package farside

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxBytes bounds the far side's words in an error as quoted, so a body of
// control characters, each quoted as four bytes, is held to the same size as
// plain text. Three hundred bytes holds any refusal sentence seen from a real
// device or API and keeps an error one readable line. A cut adds "..." and
// the quotes, at most five bytes more.
const MaxBytes = 300

// NoOutput is what Output says for a command that wrote nothing, so an
// error still tells the reader that the far side was silent rather than
// ending in an empty pair of quotes.
const NoOutput = "no output"

// Text returns what body says, fit for an error: Words, quoted, and cut to
// MaxBytes on a character boundary, marked with "..." when cut. An empty
// body is `""`, which says the far side answered with nothing.
func Text(body string) string {
	return Masked(body, nil)
}

// Masked is Text with mask applied to the far side's words before they are
// bounded. A nil mask masks nothing.
func Masked(body string, mask func(string) string) string {
	words := extract(body)
	// The mask sees the words with their lines intact, since a masking rule
	// such as pkg/netcfg.Mask reads configuration one line at a time, and
	// sees all of them, so a secret is never cut by the bound first.
	if mask != nil {
		words = mask(words)
	}
	return quote(collapse(words))
}

// Output is Text for a command's output, stderr most often: NoOutput when
// the command wrote nothing but white space, and Text otherwise.
func Output(out string) string {
	return MaskedOutput(out, nil)
}

// OutputTail is Output keeping the end of out rather than its start, for a
// command whose reason for failing comes last: a program's stderr after its
// progress or warnings (container.exec's command, curl), or tar's last error
// before its summary line. A cut is marked with "..." at the front.
func OutputTail(out string) string {
	if strings.TrimSpace(out) == "" {
		return NoOutput
	}
	return quoteTail(collapse(extract(out)))
}

// MaskedOutput is Output with mask applied as Masked applies it.
func MaskedOutput(out string, mask func(string) string) string {
	if strings.TrimSpace(out) == "" {
		return NoOutput
	}
	return Masked(out, mask)
}

// Words returns the far side's own words in body, unbounded and unquoted:
// the first error field of a JSON answer (see jsonWords), the fault text of
// an XML answer (see xmlWords), the text of an HTML page, or else the whole
// body, each with every run of white space made one space. It is for a
// caller that classifies the text before quoting it; anything put into an
// error goes through Text.
func Words(body string) string {
	return collapse(extract(body))
}

// extract is Words before white space is collapsed: the reason found in a
// structured answer, or the whole body, with its lines intact.
func extract(body string) string {
	s := strings.TrimSpace(body)
	switch {
	case strings.HasPrefix(s, "{"), strings.HasPrefix(s, "["):
		if w, ok := jsonWords(s); ok {
			s = w
		}
	case strings.HasPrefix(s, "<"):
		if w, ok := xmlWords(s); ok {
			s = w
		}
	}
	return s
}

// collapse makes every run of white space in s one space and trims both
// ends, so an answer over many lines reads as one.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// quote quotes s as strconv.Quote does, so a control character or an
// invalid byte is shown escaped rather than sent, and cuts the quoted text at
// MaxBytes, never inside one character's escape, marking a cut with "...".
// strconv.Quote escapes each character on its own, so quoting s one
// character at a time gives the same text, which is what lets the cut be
// measured in quoted bytes.
func quote(s string) string {
	if q := strconv.Quote(s); len(q) <= MaxBytes+2 {
		return q
	}
	var b strings.Builder
	b.WriteByte('"')
	used := 0
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		piece := strconv.Quote(s[i : i+size])
		piece = piece[1 : len(piece)-1]
		if used+len(piece) > MaxBytes {
			break
		}
		b.WriteString(piece)
		used += len(piece)
		i += size
	}
	b.WriteString(`..."`)
	return b.String()
}

// quoteTail is quote keeping the last MaxBytes quoted bytes of s rather
// than the first, never inside one character's escape, marking a cut with
// "..." after the opening quote.
func quoteTail(s string) string {
	if q := strconv.Quote(s); len(q) <= MaxBytes+2 {
		return q
	}
	var pieces []string
	used := 0
	for i := len(s); i > 0; {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		piece := strconv.Quote(s[i-size : i])
		piece = piece[1 : len(piece)-1]
		if used+len(piece) > MaxBytes {
			break
		}
		pieces = append(pieces, piece)
		used += len(piece)
		i -= size
	}
	slices.Reverse(pieces)
	return `"...` + strings.Join(pieces, "") + `"`
}

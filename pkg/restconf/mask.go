// Package restconf: masking the client's own credential out of whatever the
// device answers, before any of it reaches an error, a warning or a stat. A
// device can echo what it was sent (a 401 or 422 quoting the Authorization
// header or the password, FAILURE_PATTERNS 626), and pkg/netcfg's masking reads
// only a value's shape, so the client masks each value it holds, in every
// form it could come back in, first.
package restconf

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// identifierSecrets are the flattened credential's keys that name rather than
// prove (a login name, a host), which are left in an answer.
var identifierSecrets = map[string]bool{
	wire.SecretUsername:     true,
	wire.SecretSeedUsername: true,
	wire.SecretSeedHost:     true,
}

// secretForms are the forms the client's credential could come back in:
// each secret value, the basic-auth token and both Authorization header
// values, each also as JSON and XML escape it, longest first, so a longer
// form is masked before a shorter one inside it.
func (c *Client) secretForms() []string {
	seen := map[string]bool{}
	var forms []string
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		forms = append(forms, v)
		if raw, err := json.Marshal(v); err == nil {
			// The JSON string's inside, without its quotes.
			if inner := string(raw[1 : len(raw)-1]); !seen[inner] {
				seen[inner] = true
				forms = append(forms, inner)
			}
		}
		// XML text as Go escapes it (quotes as &#34;), and as most servers
		// do, escaping only what text content must.
		var b strings.Builder
		escaped := []string{strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(v)}
		if xml.EscapeText(&b, []byte(v)) == nil {
			escaped = append(escaped, b.String())
		}
		for _, e := range escaped {
			if !seen[e] {
				seen[e] = true
				forms = append(forms, e)
			}
		}
	}
	for key, value := range c.secrets {
		if !identifierSecrets[key] {
			add(value)
		}
	}
	for _, value := range c.extraSecrets {
		add(value)
	}
	if user, pass := c.secrets[wire.SecretUsername], c.secrets[wire.SecretPassword]; pass != "" {
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		add(token)
		add("Basic " + token)
		add("Bearer " + pass)
	}
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	return forms
}

// maskSecrets is text with every form of the client's credential replaced by
// its placeholder. A form of at least netcfg.MinLearnedLength bytes is
// replaced wherever it stands; a shorter one only where it stands as a whole
// word (no letter or digit beside it), since a short password replaced
// everywhere would cut ordinary words apart ("p" inside "application") and
// leave the answer unreadable, while an echo of it stands as its own word.
func (c *Client) maskSecrets(text string) string {
	for _, form := range c.secretForms() {
		if len(form) >= netcfg.MinLearnedLength {
			text = strings.ReplaceAll(text, form, netcfg.Placeholder(form))
			continue
		}
		text = replaceWord(text, form, netcfg.Placeholder(form))
	}
	return text
}

// replaceWord is text with each occurrence of word that has no letter or
// digit directly before or after it replaced by with.
func replaceWord(text, word, with string) string {
	var b strings.Builder
	for {
		i := strings.Index(text, word)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		end := i + len(word)
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[end:])
		b.WriteString(text[:i])
		if (i == 0 || !wordRune(before)) && (end == len(text) || !wordRune(after)) {
			b.WriteString(with)
		} else {
			b.WriteString(word)
		}
		text = text[end:]
	}
}

// wordRune reports whether r is a letter or a digit.
func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// MaskAlso adds values (a named secret a write resolved into its document)
// to what the client masks by value in every answer, beside its credential.
func (c *Client) MaskAlso(values ...string) {
	c.extraSecrets = append(c.extraSecrets, values...)
}

// Mask is text with every value the client masks replaced by its
// placeholder: its credential in each form it could be echoed in, and what
// MaskAlso added. A caller recording a device's answer masks it here first,
// then by shape (pkg/netcfg). A structured answer is masked by its decoded
// values, never across its text: one whole JSON value value by value
// (netcfg.EditJSON), so a short credential equal to a number is replaced as
// the whole value and the answer still parses (CVD run 11, 2026-11-15, showed
// what a replacement across the text does to a document); a well-formed XML
// document by the decoded text of each element (netcfg.EditXML), so a
// credential the device escaped as &apos;, &quot; or a numeric reference is
// found, and one in an attribute returns an error and none of the answer.
// Any other answer is masked as text.
func (c *Client) Mask(text string) (string, error) {
	switch {
	case netcfg.WholeJSON(text):
		return netcfg.EditJSON(text, c.wholeValue)
	case netcfg.WellFormedXML(text) && strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), "<"):
		return netcfg.EditXML(text, c.maskSecrets)
	}
	return c.maskSecrets(text), nil
}

// wholeValue is one value of a JSON answer, masked: a string as text is
// (maskSecrets), so a device's message keeps its words, and a number any
// credential form stands in replaced whole by its own placeholder, since
// what would be left beside one is no number.
func (c *Client) wholeValue(v string, number bool) string {
	masked := c.maskSecrets(v)
	if !number || masked == v {
		return masked
	}
	return netcfg.Placeholder(v)
}

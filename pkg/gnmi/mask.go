// Package gnmi: masking the client's own credential out of whatever the
// device answers, before any of it reaches an error, a warning or a stat. A
// device can echo what it was sent (a refusal quoting the password it was
// given), and pkg/netcfg's masking reads only a value's shape, so the client
// masks each value it holds, in every form it could come back in, first, and
// then by shape.
package gnmi

import (
	"encoding/json"
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
	// A certificate is published in every handshake; its key is masked.
	wire.SecretCertificatePEM: true,
}

// secretForms are the forms the client's credential could come back in: each
// secret value, the bearer header value, and each as JSON escapes it, longest
// first, so a longer form is masked before a shorter one inside it.
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
	}
	for key, value := range c.secrets {
		if !identifierSecrets[key] {
			add(value)
		}
	}
	c.mu.Lock()
	extra := append([]string(nil), c.extraSecrets...)
	c.mu.Unlock()
	for _, value := range extra {
		add(value)
	}
	if token := c.secrets[wire.SecretBearerToken]; token != "" {
		add("Bearer " + token)
	}
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	return forms
}

// maskSecrets is text with every form of the client's credential replaced by
// its placeholder, then masked by shape (pkg/netcfg.Mask). A form of at least
// netcfg.MinLearnedLength bytes is replaced wherever it stands; a shorter one
// only where it stands as a whole word (no letter or digit beside it), since a
// short password replaced everywhere would cut ordinary words apart.
func (c *Client) maskSecrets(text string) string {
	for _, form := range c.secretForms() {
		if len(form) >= netcfg.MinLearnedLength {
			text = strings.ReplaceAll(text, form, netcfg.Placeholder(form))
			continue
		}
		text = replaceWord(text, form, netcfg.Placeholder(form))
	}
	return netcfg.Mask(text)
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

// MaskAlso adds values (a named secret a write resolved into its value) to
// what the client masks by value in every answer, beside its credential.
func (c *Client) MaskAlso(values ...string) {
	c.mu.Lock()
	c.extraSecrets = append(c.extraSecrets, values...)
	c.mu.Unlock()
}

// Mask is text with every value the client masks replaced by its
// placeholder: its credential in each form it could be echoed in, and what
// MaskAlso added, then by shape. A whole JSON document is masked value by
// value (netcfg.EditJSON), so a short credential equal to a number is
// replaced as the whole value and the document still parses; any other text
// is masked as text.
func (c *Client) Mask(text string) (string, error) {
	if netcfg.WholeJSON(text) {
		return netcfg.EditJSON(text, c.wholeValue)
	}
	return c.maskSecrets(text), nil
}

// wholeValue is one value of a JSON document, masked: a string as text is,
// and a number any credential form stands in replaced whole by its own
// placeholder, since what would be left beside one is no number.
func (c *Client) wholeValue(v string, number bool) string {
	masked := c.maskSecrets(v)
	if !number || masked == v {
		return masked
	}
	return netcfg.Placeholder(v)
}

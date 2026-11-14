// Package netcfg: masking a command's output whatever form the device printed
// it in. A show command's output is CLI text by default, but IOS XE also
// prints its configuration as XML (show running-config | format netconf-xml)
// or JSON (| format restconf-json), where a secret sits in an element or a
// field rather than after a word, and the CLI text masker reads none of it:
// measured on a Catalyst 8000V (IOS XE 17.15.1a, 2026-11-14), both forms
// returned the enable secret and every local user's secret as raw type 9
// hashes, which a blind agent's scratch file then held (FAILURE_PATTERNS
// 1247).
package netcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// cryptToken matches a crypt-shaped value anywhere in text: "$", an id of one
// or two characters, "$", then either a salt and a hash each after its own
// "$" (IOS type 5 $1$salt$hash, yescrypt $y$params$salt$hash, an optional
// rounds=N first as sha512-crypt writes it), or one run of at least eight
// characters (IOS type 9 $9$salt$hash read whole, Junos $9$). "$1$ is" is not
// one: a space follows the id.
var cryptToken = regexp.MustCompile(
	`\$[0-9A-Za-z]{1,2}\$(?:rounds=[0-9]+\$)?[./0-9A-Za-z]{2,}(?:\$[./0-9A-Za-z]{2,})+` +
		`|\$[0-9A-Za-z]{1,2}\$[./0-9A-Za-z]{8,}`)

// structuredSecret matches a secret-shaped field or element written out in
// text that did not parse as a whole document: "secret": "x", "password":"x",
// <secret>x</secret>, <pre-shared-key>x</pre-shared-key>. The name is checked
// with secretName after the match; group 2 or 4 holds the value.
var structuredSecret = regexp.MustCompile(
	`"(?:[A-Za-z0-9_.-]+:)?([A-Za-z0-9_.-]+)"\s*:\s*"((?:[^"\\]|\\.)*)"` +
		`|<(?:[A-Za-z0-9_.-]+:)?([A-Za-z0-9_.-]+)>([^<]*)</`)

// errJSONSecretNotFound is a secret the JSON walk found that does not appear
// in the text as the JSON encoder writes it (the device escaped it another
// way), so it cannot be replaced in place.
var errJSONSecretNotFound = errors.New("a secret in the JSON output is escaped in a way masking cannot replace in place, so the output is not returned")

// errJSONDuplicateKey is an object holding one key twice, which a decoder
// collapses to the last value, so the walk would see only one of them.
var errJSONDuplicateKey = errors.New("the JSON output holds an object with a key twice, which masking cannot read whole, so the output is not returned")

// MaskOutput masks every secret in out, a command's output, by the form the
// device printed it in, and returns each secret it replaced, once each, for
// the run's masking set:
//
//   - output that is one whole XML document is read through MaskXML;
//   - output that is one whole JSON value is read by its fields: the string
//     leaves of a secret-named field (the names MaskXML reads, after any
//     "module:" prefix RFC 7951 puts on a field), and of every field such a
//     field encloses but its metadata (an encryption type, an id), are
//     secrets, and every other string leaf is read by the CLI text masker,
//     as MaskXML reads its leaves;
//   - anything else, a document cut short or text before or after one
//     included, is CLI text, read by MaskFound and, for what a structured
//     form left in it, by the "secret": "x" and <secret>x</secret> shapes;
//
// and in every form a crypt-shaped value is masked wherever it stands. Every
// other byte stays where it was. A whole JSON value holding a key twice, or a
// secret escaped so it cannot be replaced in place, returns an error and none
// of the text.
func MaskOutput(out string) (masked string, found []string, err error) {
	trimmed := strings.TrimLeft(out, " \t\r\n")
	switch {
	case strings.HasPrefix(trimmed, "<") && wellFormed(out) == nil:
		masked, found, err = MaskXML(out)
		if err != nil {
			return "", nil, fmt.Errorf("the output reads as XML that masking cannot vouch for: %w", err)
		}
	case (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && wholeJSON(out):
		masked, found, err = maskJSON(out)
		if err != nil {
			return "", nil, err
		}
	default:
		masked, found = MaskFound(out)
		var shaped []string
		masked, shaped = maskStructuredShapes(masked)
		found = append(found, shaped...)
	}
	masked, crypt := maskCrypt(masked)
	return masked, append(found, crypt...), nil
}

// wholeJSON reports whether out is exactly one JSON value, with nothing but
// white space after it.
func wholeJSON(out string) bool {
	dec := json.NewDecoder(strings.NewReader(out))
	var v any
	if err := dec.Decode(&v); err != nil {
		return false
	}
	_, err := dec.Token()
	return errors.Is(err, io.EOF)
}

// maskCrypt replaces every crypt-shaped value in text with its placeholder.
func maskCrypt(text string) (string, []string) {
	var found []string
	seen := map[string]bool{}
	masked := cryptToken.ReplaceAllStringFunc(text, func(v string) string {
		if !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
		return Placeholder(v)
	})
	return masked, found
}

// maskStructuredShapes masks the value of each secret-named "field": "value"
// or <element>value</element> in text that is not one whole document.
func maskStructuredShapes(text string) (string, []string) {
	var found []string
	seen := map[string]bool{}
	masked := structuredSecret.ReplaceAllStringFunc(text, func(m string) string {
		g := structuredSecret.FindStringSubmatch(m)
		name, value := g[1], g[2]
		if name == "" {
			name, value = g[3], g[4]
		}
		if !secretName(name, false) || metadataNames[strings.ToLower(name)] || strings.TrimSpace(value) == "" || strings.HasPrefix(value, MaskPrefix) {
			return m
		}
		if !seen[value] {
			seen[value] = true
			found = append(found, value)
		}
		return strings.Replace(m, value, Placeholder(value), 1)
	})
	return masked, found
}

// maskJSON masks out, one whole JSON value, in place: the secrets are found by
// walking the document and replaced where they stand in the text, so the
// device's own layout is kept.
func maskJSON(out string) (string, []string, error) {
	if err := refuseDuplicateKeys(out); err != nil {
		return "", nil, err
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", nil, fmt.Errorf("the output reads as JSON that does not parse, so masking cannot vouch for it: %w", err)
	}
	var found []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !strings.HasPrefix(v, MaskPrefix) && !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
	}
	walkJSON(doc, false, false, add, func(text string) {
		// A leaf that is not a secret's may still carry CLI text.
		_, inText := MaskFound(text)
		for _, v := range inText {
			add(v)
		}
	})
	// Longest first, so a secret holding another is replaced whole.
	sort.Slice(found, func(i, j int) bool { return len(found[i]) > len(found[j]) })
	masked := out
	for _, v := range found {
		inner := jsonInner(v)
		if !strings.Contains(masked, inner) {
			return "", nil, errJSONSecretNotFound
		}
		masked = strings.ReplaceAll(masked, inner, jsonInner(Placeholder(v)))
	}
	return masked, found, nil
}

// refuseDuplicateKeys walks out's tokens and refuses an object that holds
// one key twice: a decoder keeps only the last, so the first value would be
// left unread, and unmasked.
func refuseDuplicateKeys(out string) error {
	dec := json.NewDecoder(strings.NewReader(out))
	type frame struct {
		object bool
		keys   map[string]bool
		expect bool // the next token in an object is a key
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the output reads as JSON that does not parse, so masking cannot vouch for it: %w", err)
		}
		top := func() *frame {
			if len(stack) == 0 {
				return nil
			}
			return stack[len(stack)-1]
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				if f := top(); f != nil && f.object {
					f.expect = true
				}
				stack = append(stack, &frame{object: t == '{', keys: map[string]bool{}, expect: t == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
			}
		default:
			f := top()
			if f == nil || !f.object {
				continue
			}
			if f.expect {
				key, _ := t.(string)
				if f.keys[key] {
					return errJSONDuplicateKey
				}
				f.keys[key] = true
				f.expect = false
			} else {
				f.expect = true
			}
		}
	}
}

// walkJSON calls secret for each string leaf of v that is a secret (under a
// secret-named field, or enclosed by one with no metadata-named field
// between) and text for every other string leaf. inSNMP is set inside any
// field whose name mentions snmp, where a community is a secret.
func walkJSON(v any, inSecret, inSNMP bool, secret, text func(string)) {
	switch t := v.(type) {
	case map[string]any:
		for key, e := range t {
			name := key
			if i := strings.LastIndexByte(name, ':'); i >= 0 {
				name = name[i+1:]
			}
			lower := strings.ToLower(name)
			snmp := inSNMP || strings.Contains(lower, "snmp")
			if metadataNames[lower] {
				// Metadata stops secrecy passing down (an encryption type
				// is not the secret), not the walk: a secret-named field
				// below it is still read.
				walkJSON(e, false, snmp, secret, text)
				continue
			}
			walkJSON(e, inSecret || secretName(name, snmp), snmp, secret, text)
		}
	case []any:
		for _, e := range t {
			walkJSON(e, inSecret, inSNMP, secret, text)
		}
	case string:
		if inSecret {
			secret(t)
		} else {
			text(t)
		}
	}
}

// jsonInner is s as it stands between the quotes of a JSON string literal,
// without HTML escaping, the way a device writes one.
func jsonInner(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // #nosec G104 -- encoding a Go string into a buffer cannot fail
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(b.String(), "\n"), `"`), `"`)
}

// Package netcfg: editing a JSON document one token at a time, so masking
// replaces whole values and the document still parses. Replacing a found
// value wherever its text stood cut other values apart: CVD run 11
// (2026-11-15) recorded a Tunnel100 whose GRE key, 100, was masked in place,
// and then every "100" in the document with it, the tunnel's name and NHRP
// network-id among them as bare placeholders where JSON wants a number, and
// 10.255.100.1 as 10.255.<placeholder>.1.
package netcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// jsonLeaf is one string or number token of a JSON document: where it stands
// in the text, its value (a string decoded, a number as written), whether it
// is an object's key, and whether its place makes it a secret's value (under
// a secret-named field, or enclosed by one with no metadata-named field
// between, the rule MaskXML reads elements by).
type jsonLeaf struct {
	start, end int
	value      string
	number     bool
	key        bool
	secret     bool
}

// jsonFrame is one open object or array during jsonLeaves' walk.
type jsonFrame struct {
	object bool
	keys   map[string]bool
	// expect is set when an object's next token is a key.
	expect bool
	// secret and snmp are the container's own place: a value inside an
	// array takes them.
	secret, snmp bool
	// valueSecret and valueSNMP are the place the current key gives its
	// value, in an object.
	valueSecret, valueSNMP bool
}

// jsonLeaves walks doc, one whole JSON value (a second value after it is
// refused), and returns its string and number tokens in order. An object holding one key twice is refused: a
// reader keeps only one of the two, so the other would be read by nobody.
func jsonLeaves(doc string) ([]jsonLeaf, error) {
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.UseNumber()
	var stack []*jsonFrame
	var leaves []jsonLeaf
	prev, tokens := 0, 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) && tokens > 0 {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the output reads as JSON that does not parse, so masking cannot vouch for it: %w", err)
		}
		if tokens > 0 && len(stack) == 0 {
			return nil, errors.New("the output holds more than one JSON value, so masking cannot vouch for it")
		}
		tokens++
		end := int(dec.InputOffset())
		start := tokenStart(doc, prev, end)
		prev = end
		var top *jsonFrame
		if n := len(stack); n > 0 {
			top = stack[n-1]
		}
		// The place a value here takes.
		secret, snmp := false, false
		switch {
		case top != nil && top.object:
			secret, snmp = top.valueSecret, top.valueSNMP
		case top != nil:
			secret, snmp = top.secret, top.snmp
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				if top != nil && top.object {
					top.expect = true
				}
				stack = append(stack, &jsonFrame{object: d == '{', keys: map[string]bool{}, expect: d == '{', secret: secret, snmp: snmp})
			default:
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if top != nil && top.object && top.expect {
			key, _ := tok.(string)
			if top.keys[key] {
				return nil, errors.New("the JSON output holds an object with a key twice, which masking cannot read whole, so the output is not returned")
			}
			top.keys[key] = true
			top.expect = false
			top.valueSecret, top.valueSNMP = keyPlace(key, top.secret, top.snmp)
			leaves = append(leaves, jsonLeaf{start: start, end: end, value: key, key: true})
			continue
		}
		if top != nil && top.object {
			top.expect = true
		}
		switch v := tok.(type) {
		case string:
			leaves = append(leaves, jsonLeaf{start: start, end: end, value: v, secret: secret})
		case json.Number:
			leaves = append(leaves, jsonLeaf{start: start, end: end, value: v.String(), number: true, secret: secret})
		}
	}
	if len(stack) != 0 {
		return nil, errors.New("the output reads as JSON that ends inside a value, so masking cannot vouch for it")
	}
	return leaves, nil
}

// keyPlace is the place a field named key gives its value, inside a
// container whose own place is secret and snmp: after any "module:" prefix
// RFC 7951 puts on a name, a metadata name (an encryption type, an id) is
// never a secret's, and any other is one when its container is or its name
// is a secret's; anything under a name mentioning snmp is in an SNMP subtree,
// where a community is a secret.
func keyPlace(key string, secret, snmp bool) (bool, bool) {
	name := key
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	lower := strings.ToLower(name)
	snmp = snmp || strings.Contains(lower, "snmp")
	if metadataNames[lower] {
		return false, snmp
	}
	return secret || secretName(name, snmp), snmp
}

// tokenStart is where the token ending at end begins, after the white space
// and the separators (a colon, a comma) between it and the token before,
// which ended at prev. None of those is a quote, so a string's start is its
// opening quote.
func tokenStart(doc string, prev, end int) int {
	for i := prev; i < end; i++ {
		switch doc[i] {
		case ' ', '\t', '\r', '\n', ',', ':':
		default:
			return i
		}
	}
	return end
}

// jsonString is s as a JSON string literal, without HTML escaping, as a
// device writes one.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // #nosec G104 -- encoding a Go string into a buffer cannot fail
	return strings.TrimSuffix(b.String(), "\n")
}

// rewriteJSON is doc with each leaf whose index edits names written as that
// edit's text, a JSON string literal; every other byte stays where it was.
func rewriteJSON(doc string, leaves []jsonLeaf, edits map[int]string) string {
	var b strings.Builder
	at := 0
	for i, l := range leaves {
		text, ok := edits[i]
		if !ok {
			continue
		}
		b.WriteString(doc[at:l.start])
		b.WriteString(jsonString(text))
		at = l.end
	}
	b.WriteString(doc[at:])
	return b.String()
}

// WholeJSON reports whether text is exactly one JSON value, with nothing but
// white space after it.
func WholeJSON(text string) bool {
	dec := json.NewDecoder(strings.NewReader(text))
	var v any
	if err := dec.Decode(&v); err != nil {
		return false
	}
	_, err := dec.Token()
	return errors.Is(err, io.EOF)
}

// EditJSON is doc, one whole JSON value, with edit applied to each key and
// string value (decoded, number false) and each number (as written, number
// true), and every value edit changed written back whole, as a JSON string:
// a number edit changes becomes a string, since the text that replaced it is
// no number. Every other byte
// stays where it was, so the result parses as doc did, which is checked all
// the same. A caller masking by value (pkg/restconf's credential masking)
// replaces values through it, never across the document's text.
func EditJSON(doc string, edit func(value string, number bool) string) (string, error) {
	leaves, err := jsonLeaves(doc)
	if err != nil {
		return "", err
	}
	edits := map[int]string{}
	for i, l := range leaves {
		if v := edit(l.value, l.number); v != l.value {
			edits[i] = v
		}
	}
	out := rewriteJSON(doc, leaves, edits)
	if err := KeepsForm(doc, out); err != nil {
		return "", err
	}
	return out, nil
}

// KeepsForm returns an error when masked, the masked copy of out, no longer
// reads as the form out did: out one whole JSON value and masked not, or out
// a well-formed XML document and masked not. A copy a reader cannot parse is
// withheld rather than recorded: a backup nobody can restore from, or a diff
// an agent reads wrongly, is worse than none.
func KeepsForm(out, masked string) error {
	trimmed := strings.TrimLeft(out, " \t\r\n")
	switch {
	case WholeJSON(out) && !WholeJSON(masked):
		return errors.New("masking would leave the JSON output unreadable, so the output is not returned")
	case strings.HasPrefix(trimmed, "<") && wellFormed(out) == nil && wellFormed(masked) != nil:
		return errors.New("masking would leave the XML output unreadable, so the output is not returned")
	}
	return nil
}

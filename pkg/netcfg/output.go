// Package netcfg: masking a command's output whatever form the device printed
// it in. A show command's output is CLI text by default, but IOS XE also
// prints its configuration as XML (show running-config | format netconf-xml)
// or JSON (| format restconf-json), where a secret sits in an element or a
// field rather than after a word, and the CLI text masker reads none of it:
// measured on a Catalyst 8000V (IOS XE 17.15.1a, 2026-11-14), both forms
// returned the enable secret and every local user's secret as raw type 9
// hashes, which a blind agent's scratch file then held.
package netcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// cryptToken matches a crypt-shaped value anywhere in text: "$" an id of one
// or two characters "$" and then the salt and hash, as IOS type 5 ($1$), 8
// ($8$) and 9 ($9$), Linux ($5$, $6$, $y$) and Junos ($9$) write them. At
// least eight characters follow the id, so a price or a shell variable is not
// read as one.
var cryptToken = regexp.MustCompile(`\$[0-9A-Za-z]{1,2}\$[./0-9A-Za-z]{8,}(?:\$[./0-9A-Za-z]*)*`)

// MaskOutput masks every secret in out, a command's output, by the form the
// device printed it in, and returns each secret it replaced, once each, for
// the run's masking set:
//
//   - output that opens with "<" is read as XML, through MaskXML;
//   - output that opens with "{" or "[" is read as JSON: the string leaves of
//     a secret-named field (the names MaskXML reads, after any "module:"
//     prefix RFC 7951 puts on a field), and of every field such a field
//     encloses but its metadata (an encryption type, an id), are secrets;
//   - anything else is CLI text, read by MaskFound;
//
// and in every form a crypt-shaped value is masked wherever it stands.
// Every other byte stays where it was. It fails closed: output that opens as
// XML or JSON and does not read as one returns an error and none of the text,
// since nothing could then vouch for what it holds.
func MaskOutput(out string) (masked string, found []string, err error) {
	switch trimmed := strings.TrimLeft(out, " \t\r\n"); {
	case strings.HasPrefix(trimmed, "<"):
		masked, found, err = MaskXML(out)
		if err != nil {
			return "", nil, fmt.Errorf("the output reads as XML that masking cannot vouch for: %w", err)
		}
	case strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "["):
		masked, found, err = maskJSON(out)
		if err != nil {
			return "", nil, err
		}
	default:
		masked, found = MaskFound(out)
	}
	masked, crypt := maskCrypt(masked)
	return masked, append(found, crypt...), nil
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

// errJSONSecretNotFound is a secret the JSON walk found that does not appear
// in the text as the JSON encoder writes it (the device escaped it another
// way), so it cannot be replaced in place.
var errJSONSecretNotFound = errors.New("a secret in the JSON output is escaped in a way masking cannot replace in place, so the output is not returned")

// maskJSON masks out, JSON output, in place: the secrets are found by walking
// the decoded document and replaced where they stand in the text, so the
// device's own layout is kept.
func maskJSON(out string) (string, []string, error) {
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", nil, fmt.Errorf("the output reads as JSON that does not parse, so masking cannot vouch for it: %w", err)
	}
	var found []string
	seen := map[string]bool{}
	walkJSON(doc, false, false, func(v string) {
		if v != "" && !strings.HasPrefix(v, MaskPrefix) && !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
	})
	// Longest first, so a secret holding another is replaced whole.
	sort.Slice(found, func(i, j int) bool { return len(found[i]) > len(found[j]) })
	masked := out
	for _, v := range found {
		quoted, err := jsonQuote(v)
		if err != nil || !strings.Contains(masked, quoted) {
			return "", nil, errJSONSecretNotFound
		}
		placeholder, _ := jsonQuote(Placeholder(v))
		masked = strings.ReplaceAll(masked, quoted, placeholder)
	}
	return masked, found, nil
}

// walkJSON calls secret for each string leaf of v that is a secret: under a
// secret-named field, or enclosed by one with no metadata-named field
// between. inSNMP is set inside any field whose name mentions snmp, where a
// community is a secret.
func walkJSON(v any, inSecret, inSNMP bool, secret func(string)) {
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
				walkJSON(e, false, snmp, secret)
				continue
			}
			walkJSON(e, inSecret || secretName(name, snmp), snmp, secret)
		}
	case []any:
		for _, e := range t {
			walkJSON(e, inSecret, inSNMP, secret)
		}
	case string:
		if inSecret {
			secret(t)
		}
	}
}

// jsonQuote is s as a JSON string literal, without HTML escaping, the way a
// device writes one.
func jsonQuote(s string) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

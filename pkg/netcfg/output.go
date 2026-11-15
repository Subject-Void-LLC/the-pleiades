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
	"fmt"
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

// MaskOutput masks every secret in out, a command's output, by the form the
// device printed it in, and returns each secret it replaced, once each, for
// the run's masking set:
//
//   - output that is one whole XML document is read through MaskXML;
//   - output that is one whole JSON value is read by its fields (maskJSON):
//     the string and number leaves of a secret-named field (the names
//     MaskXML reads, after any "module:" prefix RFC 7951 puts on a field),
//     and of every field such a field encloses but its metadata (an
//     encryption type, an id), are secrets, and every other string leaf is
//     read by the CLI text masker, as MaskXML reads its leaves;
//   - anything else, a document cut short or text before or after one
//     included, is CLI text, read by MaskFound and, for what a structured
//     form left in it, by the "secret": "x" and <secret>x</secret> shapes;
//
// and in every form a crypt-shaped value is masked wherever it stands. Every
// other byte stays where it was, and a value is only ever replaced whole, so
// a whole JSON value or XML document still reads as one. A whole JSON value
// holding a key twice, or a masked copy that no longer reads as the form the
// output was in, returns an error and none of the text.
func MaskOutput(out string) (masked string, found []string, err error) {
	trimmed := strings.TrimLeft(out, " \t\r\n")
	switch {
	case strings.HasPrefix(trimmed, "<") && wellFormed(out) == nil:
		masked, found, err = MaskXML(out)
		if err != nil {
			return "", nil, fmt.Errorf("the output reads as XML that masking cannot vouch for: %w", err)
		}
	case (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && WholeJSON(out):
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
	// Each form's masking keeps the document whole; checked all the same, so
	// a copy nobody could read back is never returned.
	if err := KeepsForm(out, masked); err != nil {
		return "", nil, err
	}
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

// maskJSON masks out, one whole JSON value, leaf by leaf, so every value it
// replaces is replaced whole and the document still parses: a secret leaf (a
// string or a number under a secret-named field, jsonLeaves' rule) becomes
// its placeholder, as a JSON string; any other string leaf is read by the
// CLI text masker, which a leaf carrying configuration lines needs; and a
// secret found either way that is long enough to mask by value (Learnable)
// is masked inside every other string leaf that repeats it. A secret found
// is never replaced across the document's text: a short one, or a number (a
// GRE key of 100), would cut every name, address and number holding its
// characters apart (CVD run 11).
func maskJSON(out string) (string, []string, error) {
	leaves, err := jsonLeaves(out)
	if err != nil {
		return "", nil, err
	}
	var found []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !strings.HasPrefix(v, MaskPrefix) && !seen[v] {
			seen[v] = true
			found = append(found, v)
		}
	}
	edits := map[int]string{}
	for i, l := range leaves {
		switch {
		case l.key:
		case l.secret:
			if strings.TrimSpace(l.value) != "" && !strings.HasPrefix(l.value, MaskPrefix) {
				add(l.value)
				edits[i] = Placeholder(l.value)
			}
		case !l.number:
			masked, inText := MaskFound(l.value)
			for _, v := range inText {
				add(v)
			}
			if masked != l.value {
				edits[i] = masked
			}
		}
	}
	// Longest first, so a secret holding another is replaced whole.
	learned := Learnable(found)
	sort.Slice(learned, func(i, j int) bool { return len(learned[i]) > len(learned[j]) })
	for i, l := range leaves {
		if l.number || l.secret && edits[i] != "" {
			continue
		}
		text, ok := edits[i]
		if !ok {
			text = l.value
		}
		replaced := text
		for _, v := range learned {
			replaced = strings.ReplaceAll(replaced, v, Placeholder(v))
		}
		if replaced != text {
			edits[i] = replaced
		}
	}
	return rewriteJSON(out, leaves, edits), found, nil
}

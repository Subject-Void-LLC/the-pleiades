// The one way this command prints JSON: indented, and inert on a terminal.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// writeJSON writes v to w as indented JSON followed by a newline.
//
// A --json document carries text from devices and from external programs,
// and is as likely to be read on a terminal as by a program. encoding/json
// escapes the C0 control characters but writes DEL, the C1 controls and
// the Unicode direction overrides as they are, and a terminal may act on
// them (internal/termsafe). So every character termsafe calls unsafe is
// written as a \u escape instead, which any JSON reader decodes back to
// the same text. HTML characters stay as they are, so ">=1.0.0" reads as
// itself.
func writeJSON(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}
	_, err := w.Write(inertJSON(buf.Bytes()))
	return err
}

// inertJSON returns encoded JSON with every termsafe.Unsafe character
// written as a \u escape. Such a character can only sit inside a string,
// where a \u escape means the same character, so the document decodes to
// exactly what it did before. Every unsafe character is in the Basic
// Multilingual Plane, so one \uXXXX always holds it.
func inertJSON(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if termsafe.Unsafe(r) {
			out = fmt.Appendf(out, `\u%04x`, r)
		} else {
			out = append(out, data[:size]...)
		}
		data = data[size:]
	}
	return out
}

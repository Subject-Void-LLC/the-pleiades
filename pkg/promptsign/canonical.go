// Package promptsign signs and verifies the documents a model call is
// built from (a prompt, a parameter set, a routing table) as canonical
// JSON with a detached Ed25519 signature, and decides what a use's policy
// makes of the result. A signature is provenance and integrity: it says
// who approved a document and that it is unchanged, not that what a model
// does with it is safe.
package promptsign

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

// MaxDocumentBytes bounds a document.
const MaxDocumentBytes = 1 << 20

// Canonical returns the canonical form of a JSON document: UTF-8; object
// keys sorted by code point; no white space between tokens; strings
// escaped minimally (", \ and control characters, \b \f \n \r \t in their
// short forms, the rest as \u00XX); numbers written as Go writes a
// float64 at its shortest ('f' form). It refuses, rather than resolves, what
// two readers could read differently: a duplicate key at any depth, invalid
// UTF-8, an unpaired UTF-16 surrogate escape, a number outside
// (-2^53, 2^53) or one written with an exponent, a second document, and
// nesting past maxDepth.
func Canonical(doc []byte) ([]byte, error) {
	if len(doc) > MaxDocumentBytes {
		return nil, fmt.Errorf("the document is over %d bytes", MaxDocumentBytes)
	}
	if !utf8.Valid(doc) {
		return nil, errors.New("the document is not UTF-8")
	}
	p := &parser{src: doc}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.src) {
		return nil, p.errorf("text after the document")
	}
	var out bytes.Buffer
	write(&out, v)
	return out.Bytes(), nil
}

// write writes v canonically.
func write(out *bytes.Buffer, v any) {
	switch v := v.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case number:
		f := float64(v)
		if f == 0 {
			f = 0 // -0 is written as 0
		}
		out.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
	case string:
		writeString(out, v)
	case array:
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			write(out, item)
		}
		out.WriteByte(']')
	case object:
		sorted := append(object(nil), v...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
		out.WriteByte('{')
		for i, m := range sorted {
			if i > 0 {
				out.WriteByte(',')
			}
			writeString(out, m.key)
			out.WriteByte(':')
			write(out, m.value)
		}
		out.WriteByte('}')
	}
}

// writeString writes s with the minimal escapes.
func writeString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(out, `\u%04x`, r)
				continue
			}
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
}

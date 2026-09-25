// PowerShell's error output, decoded from the CLIXML it arrives in.
package winrmexec

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
)

// clixmlHeader starts the error output powershell.exe writes as
// serialized objects rather than text, which it does whenever its error
// stream is not a console. A WinRM command's never is.
const clixmlHeader = "#< CLIXML"

// streamPrefixes are what a console prints before a record from each
// stream that has one. An Error record's text is already formatted, one
// element per line.
var streamPrefixes = map[string]string{
	"warning": "WARNING: ",
	"verbose": "VERBOSE: ",
	"debug":   "DEBUG: ",
}

// decodeCLIXML returns PowerShell's error output as the text a console
// would have shown, or stderr unchanged when it is not CLIXML.
//
// The output is not one XML document. powershell.exe writes the header,
// and then one or more <Objs> documents between which anything a native
// program wrote to the same handle appears as plain text, which can hold
// any character, '<' and '&' included. So only each <Objs> block is
// parsed, and everything outside one is kept as written. A block that
// does not parse is kept as written too: this never loses output, it
// only fails to tidy it.
//
// Within a block, a string record names its stream (<S S="Error">) and
// is kept, with a warning, verbose or debug record prefixed as a console
// prefixes it. An information record (<Obj S="information">) is dropped:
// what a console would have shown from that stream, Write-Host's output
// and Write-Information's when asked for, already reached stdout, and the
// rest a console would not show. Progress records never arrive, since the
// PowerShell mode turns progress off.
func decodeCLIXML(stderr string) string {
	body, ok := strings.CutPrefix(stderr, clixmlHeader)
	if !ok {
		return stderr
	}
	body = strings.TrimPrefix(strings.TrimPrefix(body, "\r"), "\n")

	const open, closing = "<Objs", "</Objs>"
	var b strings.Builder
	for {
		start := strings.Index(body, open)
		if start < 0 {
			break
		}
		length := strings.Index(body[start:], closing)
		if length < 0 {
			break
		}
		end := start + length + len(closing)
		b.WriteString(body[:start])
		if text, err := objsText(body[start:end]); err == nil {
			b.WriteString(text)
		} else {
			b.WriteString(body[start:end])
		}
		body = body[end:]
	}
	b.WriteString(body)
	return b.String()
}

// objsText returns the text of one <Objs> block's stream records.
func objsText(block string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(block))
	var b strings.Builder
	depth := 0
	stream := ""
	inRecord := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if depth != 0 {
				return "", io.ErrUnexpectedEOF
			}
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			// A stream record is a direct child of <Objs>; an <S> deeper
			// down is a property of some object, not output.
			if depth == 2 && t.Name.Local == "S" {
				stream = attribute(t, "S")
				inRecord = stream != ""
				b.WriteString(streamPrefixes[stream])
			}
		case xml.EndElement:
			if depth == 2 && inRecord {
				if _, prefixed := streamPrefixes[stream]; prefixed {
					b.WriteString("\r\n")
				}
				inRecord = false
			}
			depth--
		case xml.CharData:
			if inRecord {
				b.WriteString(unescapeCLIXML(string(t)))
			}
		}
	}
}

// attribute returns the value of the named attribute, or "".
func attribute(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// unescapeCLIXML decodes the _xHHHH_ escapes PowerShell writes for a
// character a CLIXML string cannot carry: a control character, a line
// break, and each half of a character outside the Basic Multilingual
// Plane, which is why consecutive escapes are decoded together as UTF-16.
// A literal "_x" is itself escaped, as _x005F_x, so text that only looks
// like an escape survives.
func unescapeCLIXML(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	var b strings.Builder
	var units []uint16
	flush := func() {
		for _, r := range utf16.Decode(units) {
			b.WriteRune(r)
		}
		units = units[:0]
	}
	for i := 0; i < len(s); {
		if unit, ok := escapeAt(s, i); ok {
			units = append(units, unit)
			i += len("_xHHHH_")
			continue
		}
		flush()
		b.WriteByte(s[i])
		i++
	}
	flush()
	return b.String()
}

// escapeAt reports whether s holds an _xHHHH_ escape at i, and its value.
func escapeAt(s string, i int) (uint16, bool) {
	const width = len("_xHHHH_")
	if i+width > len(s) || s[i] != '_' || s[i+1] != 'x' || s[i+6] != '_' {
		return 0, false
	}
	var v uint16
	for _, c := range []byte(s[i+2 : i+6]) {
		d, ok := hexDigit(c)
		if !ok {
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

// hexDigit returns the value of one hexadecimal digit.
func hexDigit(c byte) (uint16, bool) {
	switch {
	case c >= '0' && c <= '9':
		return uint16(c - '0'), true
	case c >= 'a' && c <= 'f':
		return uint16(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return uint16(c-'A') + 10, true
	}
	return 0, false
}

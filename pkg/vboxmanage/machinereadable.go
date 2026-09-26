// VBoxManage's --machinereadable output: one key=value pair a line.
package vboxmanage

import (
	"fmt"
	"strings"
)

// Pair is one key=value line.
type Pair struct {
	Key   string
	Value string
}

// Values is a --machinereadable answer, in the order VBoxManage wrote it.
// A key can appear more than once (each recording screen repeats its
// settings), so it is a list rather than a map.
type Values []Pair

// Get returns the first value for key.
func (v Values) Get(key string) (string, bool) {
	for _, p := range v {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// ParseMachineReadable reads VBoxManage's --machinereadable output.
//
// A line is key=value. A key is bare, or quoted when it holds characters
// such as '-' in a storage slot name; a value is a bare number or word, or
// a quoted string in which a backslash and a quote are each written with a
// backslash before them, as in "G:\\PleiadesLab". A line with no '=' is a
// section marker (" rec_screen0") and is skipped, as are blank lines; lines
// end in CRLF on Windows. Text after a quoted value's closing quote is part
// of the value, appended as written: a running machine's VideoMode is
// written "1024,768,32"@0,0 1. An unterminated quote is refused rather
// than read to the end of the line, since what follows it is then not
// known to be the value.
func ParseMachineReadable(text string) (Values, error) {
	var values Values
	for number, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, rest, err := readToken(line, '=')
		if err != nil {
			return nil, fmt.Errorf("vboxmanage: line %d: %w", number+1, err)
		}
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		value, tail, err := readToken(rest[1:], 0)
		if err != nil {
			return nil, fmt.Errorf("vboxmanage: line %d: %w", number+1, err)
		}
		values = append(values, Pair{Key: key, Value: value + tail})
	}
	return values, nil
}

// readToken reads a quoted or bare token from the start of s, returning it
// and what follows. A bare token ends at stop, or at the end when stop is
// zero; a quoted one ends at its closing quote.
func readToken(s string, stop byte) (string, string, error) {
	if !strings.HasPrefix(s, `"`) {
		if stop == 0 {
			return s, "", nil
		}
		if i := strings.IndexByte(s, stop); i >= 0 {
			return s[:i], s[i:], nil
		}
		return s, "", nil
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '"') {
				i++
			}
			b.WriteByte(s[i])
		case '"':
			return b.String(), s[i+1:], nil
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", fmt.Errorf("unterminated quote in %q", s)
}

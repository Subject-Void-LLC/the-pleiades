// Package promptsign: a strict JSON reader, which refuses what the
// canonical form must not resolve.
package promptsign

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// maxDepth bounds how deeply a document nests.
const maxDepth = 64

// maxSafeInteger is the largest integer every JSON reader holds exactly.
const maxSafeInteger = 1<<53 - 1

// value kinds the parser builds.
type (
	object []member
	member struct {
		key   string
		value any
	}
	array  []any
	number float64
)

// parser reads JSON strictly.
type parser struct {
	src []byte
	pos int
}

// errorf is an error at the parser's offset.
func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("offset %d: "+format, append([]any{p.pos}, args...)...)
}

// space skips JSON white space.
func (p *parser) space() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// value reads one value.
func (p *parser) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, p.errorf("the document nests deeper than %d", maxDepth)
	}
	p.space()
	if p.pos >= len(p.src) {
		return nil, p.errorf("the document ends where a value was expected")
	}
	switch c := p.src[p.pos]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		return p.string()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	default:
		for _, lit := range []struct {
			text  string
			value any
		}{{"true", true}, {"false", false}, {"null", nil}} {
			if bytes.HasPrefix(p.src[p.pos:], []byte(lit.text)) {
				p.pos += len(lit.text)
				return lit.value, nil
			}
		}
		return nil, p.errorf("%q does not begin a value", c)
	}
}

// object reads an object, refusing a key it has already read.
func (p *parser) object(depth int) (object, error) {
	p.pos++
	obj := object{}
	seen := map[string]bool{}
	p.space()
	if p.pos < len(p.src) && p.src[p.pos] == '}' {
		p.pos++
		return obj, nil
	}
	for {
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] != '"' {
			return nil, p.errorf("an object key must be a string")
		}
		key, err := p.string()
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, p.errorf("the key %q appears twice in one object", key)
		}
		seen[key] = true
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] != ':' {
			return nil, p.errorf("a key must be followed by a colon")
		}
		p.pos++
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		obj = append(obj, member{key, v})
		p.space()
		if p.pos >= len(p.src) {
			return nil, p.errorf("an object is never closed")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return obj, nil
		default:
			return nil, p.errorf("an object's members must be separated by commas")
		}
	}
}

// array reads an array.
func (p *parser) array(depth int) (array, error) {
	p.pos++
	arr := array{}
	p.space()
	if p.pos < len(p.src) && p.src[p.pos] == ']' {
		p.pos++
		return arr, nil
	}
	for {
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
		p.space()
		if p.pos >= len(p.src) {
			return nil, p.errorf("an array is never closed")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return arr, nil
		default:
			return nil, p.errorf("an array's values must be separated by commas")
		}
	}
}

// string reads a string, refusing a control character written raw and an
// unpaired surrogate escape.
func (p *parser) string() (string, error) {
	p.pos++
	var out []rune
	for {
		if p.pos >= len(p.src) {
			return "", p.errorf("a string is never closed")
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return string(out), nil
		case c < 0x20:
			return "", p.errorf("a control character inside a string must be escaped")
		case c == '\\':
			r, err := p.escape()
			if err != nil {
				return "", err
			}
			out = append(out, r)
		default:
			r, size := utf8.DecodeRune(p.src[p.pos:])
			out = append(out, r)
			p.pos += size
		}
	}
}

// escape reads one escape sequence, joining a surrogate pair.
func (p *parser) escape() (rune, error) {
	if p.pos+1 >= len(p.src) {
		return 0, p.errorf("an escape is cut short")
	}
	c := p.src[p.pos+1]
	p.pos += 2
	switch c {
	case '"', '\\', '/':
		return rune(c), nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'u':
		r, err := p.hex4()
		if err != nil {
			return 0, err
		}
		if !utf16.IsSurrogate(r) {
			return r, nil
		}
		if r >= 0xDC00 || p.pos+1 >= len(p.src) || p.src[p.pos] != '\\' || p.src[p.pos+1] != 'u' {
			return 0, p.errorf("an unpaired UTF-16 surrogate")
		}
		p.pos += 2
		low, err := p.hex4()
		if err != nil {
			return 0, err
		}
		joined := utf16.DecodeRune(r, low)
		if joined == utf8.RuneError {
			return 0, p.errorf("an unpaired UTF-16 surrogate")
		}
		return joined, nil
	}
	return 0, p.errorf("\\%c is not an escape", c)
}

// hex4 reads four hex digits.
func (p *parser) hex4() (rune, error) {
	if p.pos+4 > len(p.src) {
		return 0, p.errorf("a \\u escape needs four hex digits")
	}
	n, err := strconv.ParseUint(string(p.src[p.pos:p.pos+4]), 16, 16)
	if err != nil {
		return 0, p.errorf("a \\u escape needs four hex digits")
	}
	p.pos += 4
	return rune(n), nil
}

// number reads a number in JSON's grammar, refusing an exponent and a
// magnitude past what every reader holds exactly.
func (p *parser) number() (number, error) {
	start := p.pos
	if p.src[p.pos] == '-' {
		p.pos++
	}
	digits := func() int {
		n := 0
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	intStart := p.pos
	if digits() == 0 {
		return 0, p.errorf("a number needs a digit")
	}
	if p.src[intStart] == '0' && p.pos-intStart > 1 {
		return 0, p.errorf("a number may not have a leading zero")
	}
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		if digits() == 0 {
			return 0, p.errorf("a number's fraction needs a digit")
		}
	}
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		return 0, p.errorf("a number may not be written with an exponent")
	}
	f, err := strconv.ParseFloat(string(p.src[start:p.pos]), 64)
	if err != nil || math.Abs(f) > maxSafeInteger {
		return 0, p.errorf("the number %s is outside what every reader holds exactly", p.src[start:p.pos])
	}
	return number(f), nil
}

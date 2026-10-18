// Package docedit: the ordered JSON parser, which keeps every token as written.
package docedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// jsonParser reads JSON keeping every token's text.
type jsonParser struct {
	src []byte
	pos int
}

func (p *jsonParser) errorf(format string, a ...any) error {
	return fmt.Errorf("offset %d: %s", p.pos, fmt.Sprintf(format, a...))
}

func (p *jsonParser) space() {
	for p.pos < len(p.src) && strings.IndexByte(" \t\r\n", p.src[p.pos]) >= 0 {
		p.pos++
	}
}

func (p *jsonParser) value(depth int) (*Value, error) {
	if depth > maxDepth {
		return nil, p.errorf("nested deeper than %d", maxDepth)
	}
	if p.pos >= len(p.src) {
		return nil, p.errorf("the document ends early")
	}
	switch c := p.src[p.pos]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		raw, _, err := p.str()
		return &Value{kind: kindString, raw: raw}, err
	case c == 't' || c == 'f' || c == 'n':
		for _, lit := range []struct {
			text string
			k    kind
		}{{"true", kindBool}, {"false", kindBool}, {"null", kindNull}} {
			if bytes.HasPrefix(p.src[p.pos:], []byte(lit.text)) {
				p.pos += len(lit.text)
				return &Value{kind: lit.k, raw: lit.text}, nil
			}
		}
		return nil, p.errorf("an unknown literal")
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, p.errorf("unexpected %q", p.src[p.pos])
}

func (p *jsonParser) object(depth int) (*Value, error) {
	p.pos++
	v := &Value{kind: kindObject}
	seen := map[string]bool{}
	p.space()
	if p.pos < len(p.src) && p.src[p.pos] == '}' {
		p.pos++
		return v, nil
	}
	for {
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] != '"' {
			return nil, p.errorf("an object key must be a string")
		}
		raw, key, err := p.str()
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, p.errorf("the key %q appears twice in one object", key)
		}
		seen[key] = true
		p.space()
		if p.pos >= len(p.src) || p.src[p.pos] != ':' {
			return nil, p.errorf("a key is not followed by ':'")
		}
		p.pos++
		p.space()
		val, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.members = append(v.members, member{key: key, raw: raw, val: val})
		p.space()
		if p.pos >= len(p.src) {
			return nil, p.errorf("the object ends early")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return v, nil
		default:
			return nil, p.errorf("expected ',' or '}'")
		}
	}
}

func (p *jsonParser) array(depth int) (*Value, error) {
	p.pos++
	v := &Value{kind: kindArray}
	p.space()
	if p.pos < len(p.src) && p.src[p.pos] == ']' {
		p.pos++
		return v, nil
	}
	for {
		p.space()
		item, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.items = append(v.items, item)
		p.space()
		if p.pos >= len(p.src) {
			return nil, p.errorf("the array ends early")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return v, nil
		default:
			return nil, p.errorf("expected ',' or ']'")
		}
	}
}

// str reads a string, returning its text as written and its value.
func (p *jsonParser) str() (string, string, error) {
	start := p.pos
	p.pos++
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == '\\':
			p.pos += 2
		case c == '"':
			p.pos++
			raw := string(p.src[start:p.pos])
			var s string
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				return "", "", fmt.Errorf("offset %d: the string is not valid JSON", start)
			}
			return raw, s, nil
		case c < 0x20:
			return "", "", p.errorf("a control character in a string")
		default:
			p.pos++
		}
	}
	return "", "", p.errorf("a string ends early")
}

// number reads a number as written, checking its grammar.
func (p *jsonParser) number() (*Value, error) {
	start := p.pos
	for p.pos < len(p.src) && strings.IndexByte("+-0123456789.eE", p.src[p.pos]) >= 0 {
		p.pos++
	}
	raw := string(p.src[start:p.pos])
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("offset %d: %q is not a JSON number", start, raw)
	}
	return &Value{kind: kindNumber, raw: raw}, nil
}

// Package render: reading the three tag kinds ({{ }}, {% %} and {# #}),
// raw blocks and whitespace control out of template text.
package render

import (
	"fmt"
	"strings"
)

// The three kinds of tag, each read once its opening delimiter (and any
// marker after it) has been consumed. Each returns what follows the tag
// and the marker just inside its closing delimiter: '-', '+' or 0.

// action parses one {{ }} action. Only "-" is a marker here, as in Jinja2.
func (p *parser) action(rest string) (string, byte, error) {
	body, remainder, err := splitTag(rest, actionOpen, actionClose)
	if err != nil {
		return "", 0, err
	}
	var close byte
	if strings.HasSuffix(body, trimMarker) {
		body, close = body[:len(body)-1], trimMarker[0]
	}
	expr, err := parseExpression(body)
	if err != nil {
		return "", 0, err
	}
	p.b.add(node{kind: nodeOutput, expr: expr})
	return remainder, close, nil
}

// comment skips one {# #} comment. A comment may hold anything, delimiters
// included, up to the first "#}".
func comment(rest string) (string, byte, error) {
	end := strings.Index(rest, commentClose)
	if end < 0 {
		return "", 0, fmt.Errorf("%w: unterminated %s comment", ErrSyntax, commentOpen)
	}
	_, close := cutMarker(rest[:end])
	return rest[end+len(commentClose):], close, nil
}

// statementTag parses one {% %} tag. A raw tag also consumes the text up
// to its endraw, which it adds as literal text.
func (p *parser) statementTag(rest string) (string, byte, error) {
	body, remainder, err := splitTag(rest, blockOpen, blockClose)
	if err != nil {
		return "", 0, err
	}
	body, close := cutMarker(body)
	if strings.TrimSpace(body) == stmtRaw {
		// trim_blocks does not reach past a raw's opening tag, as in
		// Jinja2; only its own "-" does.
		if close == trimMarker[0] {
			remainder = strings.TrimLeft(remainder, whitespace)
		}
		return p.raw(remainder)
	}
	if err := p.b.statement(body); err != nil {
		return "", 0, err
	}
	return remainder, close, nil
}

// raw adds the text of a {% raw %} block, whose opening tag has been
// consumed, as literal text, and returns what follows its {% endraw %}.
// Nothing inside is parsed, so a template can write "{{" into its output.
func (p *parser) raw(rest string) (string, byte, error) {
	from := 0
	for {
		i := strings.Index(rest[from:], blockOpen)
		if i < 0 {
			return "", 0, fmt.Errorf("%w: {%% raw %%} has no {%% endraw %%}", ErrSyntax)
		}
		at := from + i
		length, open, close, ok := endRawAt(rest[at:])
		if !ok {
			// A "{%" that is not the endraw is part of the raw text.
			from = at + len(blockOpen)
			continue
		}
		text := rest[:at]
		switch {
		case open == trimMarker[0]:
			text = strings.TrimRight(text, whitespace)
		case open != keepMarker && p.opts.LstripBlocks:
			text = p.lstrip(text, len(p.source)-len(rest))
		}
		p.b.text(text)
		return rest[at+length:], close, nil
	}
}

// endRawAt reports whether s opens with an endraw tag, its length, and its
// two markers. It reads forward from the "{%" and never searches ahead for
// a "%}", so a raw block full of "{%" that are not its end is still read in
// linear time.
func endRawAt(s string) (length int, open, close byte, ok bool) {
	pos := len(blockOpen)
	if pos < len(s) && (s[pos] == trimMarker[0] || s[pos] == keepMarker) {
		open = s[pos]
		pos++
	}
	pos += len(s[pos:]) - len(strings.TrimLeft(s[pos:], whitespace))
	if !strings.HasPrefix(s[pos:], stmtEndRaw) {
		return 0, 0, 0, false
	}
	pos += len(stmtEndRaw)
	pos += len(s[pos:]) - len(strings.TrimLeft(s[pos:], whitespace))
	if pos < len(s) && (s[pos] == trimMarker[0] || s[pos] == keepMarker) {
		close = s[pos]
		pos++
	}
	if !strings.HasPrefix(s[pos:], blockClose) {
		return 0, 0, 0, false
	}
	return pos + len(blockClose), open, close, true
}

// splitTag returns the body of the tag that has already had its opening
// delimiter consumed, plus whatever follows the closing one.
//
// The closing delimiter counts only outside a quoted string and outside
// any open bracket, as Jinja2's lexer counts it, so "{{ {'a': {'b': 1}} }}"
// ends at its last "}}" and "{% set s = '%}' %}" at its second "%}". The
// scan reads each byte once, so a long tag still costs linear time.
func splitTag(rest, open, close string) (body, remainder string, err error) {
	depth := 0
	var quote byte
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case depth == 0 && strings.HasPrefix(rest[i:], close):
			return rest[:i], rest[i+len(close):], nil
		case c == '(' || c == '[' || c == '{':
			depth++
		case (c == ')' || c == ']' || c == '}') && depth > 0:
			depth--
		}
	}
	return "", "", fmt.Errorf("%w: unterminated %s", ErrSyntax, open)
}

// cutMarker removes a closing "-" or "+" from the end of a statement's or
// comment's body and returns it, or 0.
func cutMarker(body string) (string, byte) {
	if n := len(body); n > 0 && (body[n-1] == trimMarker[0] || body[n-1] == keepMarker) {
		return body[:n-1], body[n-1]
	}
	return body, 0
}

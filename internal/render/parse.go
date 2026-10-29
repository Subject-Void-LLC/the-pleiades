package render

import (
	"fmt"
	"strings"
)

// The template-level grammar: literal text with {{ expression }} actions,
// {% statement %} tags and {# comment #} blocks embedded in it. The
// expression grammar lives in expr.go and the statement grammar in
// statement.go; this file splits the source into those pieces and builds
// the tree of nodes the statements nest into.

// Delimiters this grammar recognizes.
const (
	actionOpen  = "{{"
	actionClose = "}}"

	// blockOpen and blockClose delimit a statement. Only the statements
	// statement.go lists are implemented; every other one is refused at
	// compile time by name, never passed through as text, because a loop's
	// source written into a file where its output belonged gives the author
	// no reason to suspect the renderer.
	blockOpen  = "{%"
	blockClose = "%}"

	// commentOpen and commentClose delimit a comment, which renders as
	// nothing.
	commentOpen  = "{#"
	commentClose = "#}"

	// trimMarker is Jinja2's whitespace control: just inside an opening
	// delimiter it trims the whitespace before the tag, just inside a
	// closing one the whitespace after it. AWX exports contain it, and it
	// works the same on all three kinds of tag.
	trimMarker = "-"

	// whitespace is what a trim marker removes.
	whitespace = " \t\r\n"
)

// keepMarker is Jinja2's "+": just inside a statement's or comment's
// opening delimiter it keeps the whitespace lstrip_blocks would strip
// before the tag, and just inside its closing one the newline trim_blocks
// would remove after it.
const keepMarker = '+'

// tagKind says which delimiter opened a tag.
type tagKind int

const (
	tagAction tagKind = iota
	tagStatement
	tagComment
)

// parser is one parse in progress: the source, the options it was
// compiled with, and the tree being built.
type parser struct {
	b      *builder
	opts   Options
	source string
}

// parse compiles source into a node tree, or reports why it cannot. plain
// reports a template holding no statement and no comment, the only kind
// Template.Value may evaluate as one typed expression.
//
// It is a single left-to-right pass with no backtracking, which is what
// keeps the fuzz target's runtime linear in the input size: a hostile
// input can be long, but it cannot be quadratic.
func parse(source string, opts Options) (nodes []node, plain bool, err error) {
	if len(source) > maxSourceBytes {
		return nil, false, fmt.Errorf("%w: source is %d bytes, limit is %d", ErrTooLarge, len(source), maxSourceBytes)
	}

	p := &parser{b: &builder{}, opts: opts, source: source}
	plain = true
	rest := source

	for {
		open, kind := nextTag(rest)
		if open < 0 {
			// No further tags: everything left is literal text.
			p.b.text(rest)
			nodes, err := p.b.finish()
			return nodes, plain, err
		}

		start := len(source) - len(rest)
		leading := rest[:open]
		rest = rest[open+2:]
		block := kind != tagAction

		// "{{-", "{%-" and "{#-" trim whitespace off the end of the text
		// before the tag; "{%+" and "{#+" keep it; otherwise lstrip_blocks
		// strips a statement's or comment's indentation.
		switch {
		case strings.HasPrefix(rest, trimMarker):
			rest = rest[len(trimMarker):]
			leading = strings.TrimRight(leading, whitespace)
		case block && rest != "" && rest[0] == keepMarker:
			rest = rest[1:]
		case block && opts.LstripBlocks:
			leading = p.lstrip(leading, start)
		}
		p.b.text(leading)

		var close byte
		switch kind {
		case tagAction:
			rest, close, err = p.action(rest)
		case tagComment:
			plain = false
			rest, close, err = comment(rest)
		case tagStatement:
			plain = false
			rest, close, err = p.statementTag(rest)
		}
		if err != nil {
			return nil, false, err
		}
		rest = p.after(rest, close, block)
	}
}

// after applies a tag's closing whitespace control to the text after it:
// "-" trims all of its leading whitespace, "+" keeps it, and otherwise
// trim_blocks removes the one newline that ends a statement's or comment's
// line, a "\r\n" counting as one.
func (p *parser) after(rest string, close byte, block bool) string {
	switch {
	case close == trimMarker[0]:
		return strings.TrimLeft(rest, whitespace)
	case close == keepMarker || !block || !p.opts.TrimBlocks:
		return rest
	case strings.HasPrefix(rest, "\r\n"):
		return rest[2:]
	}
	return strings.TrimPrefix(rest, "\n")
}

// lstrip is lstrip_blocks: text whose last line holds only spaces and tabs
// before a tag loses them, when that line starts a line of the source (it
// follows a newline, or the template begins with it). start is where text
// begins in the source.
func (p *parser) lstrip(text string, start int) string {
	nl := strings.LastIndexByte(text, '\n')
	lineStart := nl >= 0 || start == 0 || p.source[start-1] == '\n'
	if lineStart && strings.Trim(text[nl+1:], " \t") == "" {
		return text[:nl+1]
	}
	return text
}

// nextTag returns where the first tag in text opens and which kind it is,
// or -1 when text holds none.
//
// It scans only up to the first tag, never the whole remaining text once
// per kind, so a long template full of tags still parses in linear time.
func nextTag(text string) (int, tagKind) {
	for from := 0; ; {
		i := strings.IndexByte(text[from:], '{')
		if i < 0 || from+i+1 >= len(text) {
			return -1, tagAction
		}
		at := from + i
		switch text[at+1] {
		case '{':
			return at, tagAction
		case '%':
			return at, tagStatement
		case '#':
			return at, tagComment
		}
		from = at + 1
	}
}

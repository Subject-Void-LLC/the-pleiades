package render

import (
	"fmt"
	"strings"
)

// The template-level grammar: literal text with {{ expression }} actions
// embedded in it. The expression grammar itself lives in expr.go, which
// this file calls once per action.

// Delimiters this grammar recognizes. The refused two are listed here
// rather than being handled by falling through to literal text, because
// the whole point is that they are recognized and rejected.
const (
	actionOpen  = "{{"
	actionClose = "}}"

	// blockOpen and commentOpen are real Jinja2 syntax this subset does
	// not implement. Treating them as literal text would render a
	// statement block into an environment variable verbatim, so an author
	// who wrote a loop would see their loop's source code where they
	// expected its output and would have no reason to suspect the renderer.
	// Refusing at compile time makes the unsupported thing loud.
	blockOpen   = "{%"
	commentOpen = "{#"

	// trimMarker is Jinja2's whitespace control. AWX exports contain it,
	// so this subset honors it rather than refusing templates that
	// round-trip through AWX unchanged.
	trimMarker = "-"
)

// node is one piece of a compiled template: either a literal chunk of text
// or one action to evaluate. Exactly one of the two is meaningful, decided
// by expr being nil.
type node struct {
	// text is the literal output of this node when expr is nil.
	text string

	// expr is the expression to evaluate when this node is an action.
	expr *expression
}

// parse compiles source into a node list, or reports why it cannot.
//
// It is a single left-to-right pass with no backtracking, which is what
// keeps the fuzz target's runtime linear in the input size: a hostile
// input can be long, but it cannot be quadratic.
func parse(source string) ([]node, error) {
	if len(source) > maxSourceBytes {
		return nil, fmt.Errorf("%w: source is %d bytes, limit is %d", ErrTooLarge, len(source), maxSourceBytes)
	}

	var nodes []node
	rest := source

	for {
		open := strings.Index(rest, actionOpen)
		if open < 0 {
			// No further actions. Everything left is literal text, but it
			// still has to be checked for the refused delimiters, or a
			// template whose only content is a statement block would parse
			// clean as one text node.
			if err := rejectUnsupportedDelimiters(rest); err != nil {
				return nil, err
			}
			nodes = appendText(nodes, rest)
			return nodes, nil
		}

		leading := rest[:open]
		if err := rejectUnsupportedDelimiters(leading); err != nil {
			return nil, err
		}

		rest = rest[open+len(actionOpen):]

		// "{{-" trims whitespace off the end of the text before it.
		if strings.HasPrefix(rest, trimMarker) {
			rest = rest[len(trimMarker):]
			leading = strings.TrimRight(leading, " \t\r\n")
		}
		nodes = appendText(nodes, leading)

		body, remainder, err := splitAction(rest)
		if err != nil {
			return nil, err
		}
		rest = remainder

		// "-}}" trims whitespace off the start of the text after it.
		if strings.HasSuffix(body, trimMarker) {
			body = body[:len(body)-len(trimMarker)]
			rest = strings.TrimLeft(rest, " \t\r\n")
		}

		expr, err := parseExpression(body)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node{expr: expr})
	}
}

// splitAction returns the body of the action that has already had its
// opening delimiter consumed, plus whatever follows the closing one.
//
// A nested opening delimiter inside an action is refused rather than
// treated as part of the expression. "{{ a {{ b }}" has no reading that
// this grammar supports, and guessing one produces an expression the author
// did not write.
func splitAction(rest string) (body, remainder string, err error) {
	end := strings.Index(rest, actionClose)
	if end < 0 {
		return "", "", fmt.Errorf("%w: unterminated %s", ErrSyntax, actionOpen)
	}

	body = rest[:end]
	if strings.Contains(body, actionOpen) {
		return "", "", fmt.Errorf("%w: nested %s inside an expression", ErrSyntax, actionOpen)
	}

	return body, rest[end+len(actionClose):], nil
}

// rejectUnsupportedDelimiters refuses real Jinja2 syntax this subset does
// not implement, wherever it appears in literal text.
func rejectUnsupportedDelimiters(text string) error {
	if strings.Contains(text, blockOpen) {
		return fmt.Errorf("%w: %s statement blocks are not supported", ErrSyntax, blockOpen)
	}
	if strings.Contains(text, commentOpen) {
		return fmt.Errorf("%w: %s comments are not supported", ErrSyntax, commentOpen)
	}
	return nil
}

// appendText adds a literal node, skipping empty ones so that a template
// which is nothing but actions does not carry empty nodes between them.
func appendText(nodes []node, text string) []node {
	if text == "" {
		return nodes
	}
	return append(nodes, node{text: text})
}

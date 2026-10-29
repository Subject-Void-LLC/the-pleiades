// Package render: the node tree a compiled template is, and the builder
// that turns its tags into if, for and set nodes as the parser reads them.
package render

import "fmt"

// The node tree statements build: the shape of an if and a for, and the
// builder that nests the nodes parse reads into the block open at the
// time. statement.go reads each statement and tells the builder what to do.

// nodeKind says what one node of a compiled template is.
type nodeKind int

const (
	// nodeText is literal text, written as it is.
	nodeText nodeKind = iota

	// nodeOutput is a {{ }} expression, whose value is written.
	nodeOutput

	// nodeIf is an if statement with its elif and else branches.
	nodeIf

	// nodeFor is a for loop with its optional else.
	nodeFor

	// nodeSet is a set statement.
	nodeSet
)

// node is one piece of a compiled template. kind decides which of the
// other fields is meaningful.
type node struct {
	kind nodeKind

	// text is a nodeText's literal output.
	text string

	// expr is a nodeOutput's expression, or a nodeSet's value.
	expr *expression

	// name is the name a nodeSet binds.
	name string

	// branches are a nodeIf's if and elif branches in order, then its else
	// branch, which has no condition, when it has one.
	branches []branch

	// loop is a nodeFor's loop.
	loop *forLoop
}

// branch is one arm of an if statement. cond is nil for the else arm.
type branch struct {
	cond *expression
	body []node
}

// forLoop is one for statement.
type forLoop struct {
	// targets are the one or two names each item binds.
	targets []string
	// iter is the list the loop walks.
	iter *expression
	// body runs once per item; otherwise runs when there was none.
	body      []node
	otherwise []node
}

// openBlock is an if or for statement whose end tag has not been read.
type openBlock struct {
	n *node
	// body is where the nodes read now belong.
	body *[]node
	// inElse reports that the block's else has been read, after which an
	// elif or a second else is an error.
	inElse bool
}

// builder assembles the node tree as parse reads tags.
type builder struct {
	root  []node
	stack []*openBlock
}

// target is the node list the next node belongs to.
func (b *builder) target() *[]node {
	if len(b.stack) == 0 {
		return &b.root
	}
	return b.stack[len(b.stack)-1].body
}

// add appends one node where it belongs.
func (b *builder) add(n node) { *b.target() = append(*b.target(), n) }

// text appends literal text, skipping empty text so that a template which
// is nothing but tags does not carry empty nodes between them.
func (b *builder) text(s string) {
	if s != "" {
		b.add(node{kind: nodeText, text: s})
	}
}

// finish returns the tree, refusing a statement left open.
func (b *builder) finish() ([]node, error) {
	if len(b.stack) > 0 {
		open := stmtIf
		if b.stack[len(b.stack)-1].n.kind == nodeFor {
			open = stmtFor
		}
		return nil, fmt.Errorf("%w: {%% %s %%} has no {%% end%s %%}", ErrSyntax, open, open)
	}
	return b.root, nil
}

// push opens a block, holding the nesting bound.
func (b *builder) push(blk *openBlock) error {
	if len(b.stack) >= maxNestingDepth {
		return fmt.Errorf("%w: statements nested deeper than %d", ErrTooLarge, maxNestingDepth)
	}
	b.stack = append(b.stack, blk)
	return nil
}

// innermost returns the innermost open block, or nil.
func (b *builder) innermost() *openBlock {
	if len(b.stack) == 0 {
		return nil
	}
	return b.stack[len(b.stack)-1]
}

// top returns the innermost open block when it is of kind, or nil.
func (b *builder) top(kind nodeKind) *openBlock {
	if blk := b.innermost(); blk != nil && blk.n.kind == kind {
		return blk
	}
	return nil
}

// pop closes the innermost block and adds its node where it belongs.
func (b *builder) pop() {
	blk := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	b.add(*blk.n)
}

// openIf starts an if statement.
func (b *builder) openIf(cond *expression) error {
	n := &node{kind: nodeIf, branches: []branch{{cond: cond}}}
	return b.push(&openBlock{n: n, body: &n.branches[0].body})
}

// elif adds an elif branch to the innermost if.
func (b *builder) elif(cond *expression) error {
	blk := b.top(nodeIf)
	if blk == nil || blk.inElse {
		return fmt.Errorf("%w: elif outside an if, or after its else", ErrSyntax)
	}
	blk.n.branches = append(blk.n.branches, branch{cond: cond})
	blk.body = &blk.n.branches[len(blk.n.branches)-1].body
	return nil
}

// otherwise starts the else of the innermost if or for.
func (b *builder) otherwise() error {
	blk := b.innermost()
	if blk == nil || blk.inElse {
		return fmt.Errorf("%w: else outside an if or a for, or a second else", ErrSyntax)
	}
	blk.inElse = true
	if blk.n.kind == nodeFor {
		blk.body = &blk.n.loop.otherwise
		return nil
	}
	blk.n.branches = append(blk.n.branches, branch{})
	blk.body = &blk.n.branches[len(blk.n.branches)-1].body
	return nil
}

// end closes the innermost block when it is of kind.
func (b *builder) end(keyword string, kind nodeKind) error {
	if b.top(kind) == nil {
		return fmt.Errorf("%w: %s does not close the statement open here", ErrSyntax, keyword)
	}
	b.pop()
	return nil
}

// openFor starts a for loop.
func (b *builder) openFor(loop *forLoop) error {
	n := &node{kind: nodeFor, loop: loop}
	return b.push(&openBlock{n: n, body: &n.loop.body})
}

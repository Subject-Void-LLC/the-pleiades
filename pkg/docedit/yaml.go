// Package docedit: one value in a YAML document, set or removed through
// go.yaml.in/yaml/v3's nodes, which keep comments and key order.
package docedit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// YAMLDoc is a YAML document as nodes, with the indent it was written at.
type YAMLDoc struct {
	root   *yaml.Node
	indent int
}

// ParseYAML reads one YAML document, refusing a stream of several (an
// edit could not say which it meant), a document over MaxBytes, and a
// duplicate key, which yaml.v3 refuses itself.
func ParseYAML(data []byte) (*YAMLDoc, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("the document is over %d bytes", MaxBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	err := dec.Decode(&root)
	switch {
	case errors.Is(err, io.EOF):
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	case err != nil:
		return nil, fmt.Errorf("the document is not YAML: %w", err)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return nil, errors.New("the file holds more than one YAML document, and an edit could not say which it meant")
	}
	if len(root.Content) == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if err := depth(&root, 0); err != nil {
		return nil, err
	}
	return &YAMLDoc{root: &root, indent: yamlIndent(data)}, nil
}

// depth refuses nesting past maxDepth, and a mapping holding a key twice,
// which yaml.v3 decoding into nodes does not refuse and two readers
// resolve differently.
func depth(n *yaml.Node, d int) error {
	if d > maxDepth {
		return fmt.Errorf("nested deeper than %d", maxDepth)
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			if seen[k.Value] {
				return fmt.Errorf("line %d: the key %q appears twice in one mapping", k.Line, k.Value)
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := depth(c, d+1); err != nil {
			return err
		}
	}
	return nil
}

// yamlIndent is the indent of the first indented mapping line, 2 when
// there is none, as yaml.v3 writes it.
func yamlIndent(data []byte) int {
	for _, line := range bytes.Split(data, []byte("\n")) {
		n := 0
		for n < len(line) && line[n] == ' ' {
			n++
		}
		if n > 0 && n < len(line) && line[n] != '-' && line[n] != '#' {
			if n > 8 {
				return 2
			}
			return n
		}
	}
	return 2
}

// Encode writes the document back.
func (d *YAMLDoc) Encode() ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(d.indent)
	if err := enc.Encode(d.root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// doc is the document's top node.
func (d *YAMLDoc) doc() *yaml.Node { return d.root.Content[0] }

// Get returns the node at path, false when there is none, refusing a path
// through an alias.
func (d *YAMLDoc) Get(path []Segment) (*yaml.Node, bool, error) {
	cur := d.doc()
	for i, s := range path {
		if cur.Kind == yaml.AliasNode {
			return nil, false, fmt.Errorf("%s is an alias, which an edit cannot change alone", orRoot(path[:i]))
		}
		next, ok := yamlChild(cur, s)
		if !ok {
			return nil, false, nil
		}
		cur = next
	}
	if cur.Kind == yaml.AliasNode {
		return nil, false, fmt.Errorf("%s is an alias, which an edit cannot change alone", PathText(path))
	}
	return cur, true, nil
}

// yamlChild is the node one segment names under n.
func yamlChild(n *yaml.Node, s Segment) (*yaml.Node, bool) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == keyOf(s) {
				return n.Content[i+1], true
			}
		}
	case yaml.SequenceNode:
		if s.IsIndex && s.Index < len(n.Content) {
			return n.Content[s.Index], true
		}
	}
	return nil, false
}

// ValueNode is a decoded YAML or JSON value as a node.
func ValueNode(x any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(x); err != nil {
		return nil, fmt.Errorf("the value cannot be written as YAML: %w", err)
	}
	return &n, nil
}

// Set puts val at path, creating a mapping for each missing key on the
// way, as Value.Set does; a node set over an existing one keeps that
// one's comments.
func (d *YAMLDoc) Set(path []Segment, val *yaml.Node) error {
	if len(path) == 0 {
		return errors.New("the key path is empty")
	}
	cur := d.doc()
	for i, s := range path {
		last := i == len(path)-1
		if cur.Kind == yaml.AliasNode {
			return fmt.Errorf("%s is an alias, which an edit cannot change alone", orRoot(path[:i]))
		}
		switch cur.Kind {
		case yaml.MappingNode:
			found := false
			for j := 0; j+1 < len(cur.Content); j += 2 {
				if cur.Content[j].Value == keyOf(s) {
					found = true
					if last {
						if cur.Content[j+1].Kind == yaml.AliasNode {
							return fmt.Errorf("%s is an alias, which an edit cannot change alone", PathText(path))
						}
						keepComments(val, cur.Content[j+1])
						cur.Content[j+1] = val
						return nil
					}
					cur = cur.Content[j+1]
					break
				}
			}
			if !found {
				next := val
				if !last {
					next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				}
				cur.Content = append(cur.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: keyOf(s)}, next)
				if last {
					return nil
				}
				cur = next
			}
		case yaml.SequenceNode:
			if !s.IsIndex {
				return fmt.Errorf("%s is a list, and %q is not an index", PathText(path[:i]), s.Key)
			}
			switch {
			case s.Index < len(cur.Content):
				if last {
					keepComments(val, cur.Content[s.Index])
					cur.Content[s.Index] = val
					return nil
				}
				cur = cur.Content[s.Index]
			case s.Index == len(cur.Content):
				next := val
				if !last {
					next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				}
				cur.Content = append(cur.Content, next)
				if last {
					return nil
				}
				cur = next
			default:
				return fmt.Errorf("index %d is past the end of %s, which holds %d", s.Index, PathText(path[:i]), len(cur.Content))
			}
		default:
			return fmt.Errorf("%s is a scalar, so %s cannot be set under it", orRoot(path[:i]), s)
		}
	}
	return nil
}

// keepComments carries the comments of the node being replaced onto its
// replacement.
func keepComments(to, from *yaml.Node) {
	if to.HeadComment == "" {
		to.HeadComment = from.HeadComment
	}
	if to.LineComment == "" {
		to.LineComment = from.LineComment
	}
	if to.FootComment == "" {
		to.FootComment = from.FootComment
	}
}

// Delete removes the node at path, reporting whether there was one.
func (d *YAMLDoc) Delete(path []Segment) (bool, error) {
	if len(path) == 0 {
		return false, errors.New("the key path is empty")
	}
	parent, ok, err := d.Get(path[:len(path)-1])
	if err != nil || !ok {
		return false, err
	}
	s := path[len(path)-1]
	switch parent.Kind {
	case yaml.MappingNode:
		for j := 0; j+1 < len(parent.Content); j += 2 {
			if parent.Content[j].Value == keyOf(s) {
				parent.Content = append(parent.Content[:j], parent.Content[j+2:]...)
				return true, nil
			}
		}
	case yaml.SequenceNode:
		if s.IsIndex && s.Index < len(parent.Content) {
			parent.Content = append(parent.Content[:s.Index], parent.Content[s.Index+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// YAMLText is a node's value written on its own, without its comments,
// for a comparison and a diff: two values differing only in a comment are
// the same value.
func YAMLText(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	out, err := yaml.Marshal(uncommented(n))
	if err != nil {
		return "(unprintable: " + strconv.Quote(err.Error()) + ")"
	}
	return string(bytes.TrimSpace(out))
}

// uncommented is a deep copy of n with every comment removed.
func uncommented(n *yaml.Node) *yaml.Node {
	c := *n
	c.HeadComment, c.LineComment, c.FootComment = "", "", ""
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = uncommented(child)
	}
	return &c
}

// Package engine: YAML anchors, aliases and merge keys in a runbook, which
// every reader after the parse sees already expanded.
package engine

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// maxExpandedNodes bounds how many nodes a document's aliases may expand it
// to, so a few lines of aliases to aliases cannot grow into millions.
const maxExpandedNodes = 100000

// maxNodeDepth bounds how deep a document may nest once expanded, which is
// also what stops an alias reaching back into its own anchor.
const maxNodeDepth = 64

// mergeKey is YAML's merge key, <<: *anchor, folding an anchored mapping's
// pairs into the mapping it is written in.
const mergeKey = "<<"

// expandAliases replaces every alias in doc with a copy of the node its
// anchor marks, and folds every merge key's mappings into the mapping that
// holds it, a pair written there winning over a merged one. A runbook's
// readers (its key checks, its sugar rewrite, the decoder) then see plain
// mappings and lists, never an alias node to refuse by its kind number.
func expandAliases(doc *yaml.Node) error {
	count := 0
	expanded, err := expandNode(doc, 0, &count)
	if err != nil {
		return err
	}
	*doc = *expanded
	return nil
}

// expandNode returns a copy of n with its aliases and merge keys expanded.
func expandNode(n *yaml.Node, depth int, count *int) (*yaml.Node, error) {
	if depth > maxNodeDepth {
		return nil, fmt.Errorf("line %d: the YAML nests deeper than %d levels once its aliases are expanded (an alias inside its own anchor?)", n.Line, maxNodeDepth)
	}
	if *count++; *count > maxExpandedNodes {
		return nil, fmt.Errorf("line %d: the YAML's aliases expand it past %d nodes", n.Line, maxExpandedNodes)
	}
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return nil, fmt.Errorf("line %d: alias *%s names no anchor", n.Line, n.Value)
		}
		return expandNode(n.Alias, depth+1, count)
	}
	out := *n
	out.Anchor = ""
	out.Content = nil
	if n.Kind != yaml.MappingNode {
		for _, child := range n.Content {
			c, err := expandNode(child, depth+1, count)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, c)
		}
		return &out, nil
	}
	var merged []*yaml.Node
	written := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if key.Kind == yaml.ScalarNode && key.Value == mergeKey && key.Tag != "!!str" {
			pairs, err := mergePairs(val, depth+1, count)
			if err != nil {
				return nil, err
			}
			merged = append(merged, pairs...)
			continue
		}
		k, err := expandNode(key, depth+1, count)
		if err != nil {
			return nil, err
		}
		v, err := expandNode(val, depth+1, count)
		if err != nil {
			return nil, err
		}
		written[k.Value] = true
		out.Content = append(out.Content, k, v)
	}
	for i := 0; i+1 < len(merged); i += 2 {
		if !written[merged[i].Value] {
			written[merged[i].Value] = true
			out.Content = append(out.Content, merged[i], merged[i+1])
		}
	}
	return &out, nil
}

// mergePairs returns the key and value nodes a merge key's value folds in:
// one mapping, or a list of mappings, the earlier one winning a key both
// hold, as YAML says.
func mergePairs(val *yaml.Node, depth int, count *int) ([]*yaml.Node, error) {
	v, err := expandNode(val, depth, count)
	if err != nil {
		return nil, err
	}
	switch v.Kind {
	case yaml.MappingNode:
		return v.Content, nil
	case yaml.SequenceNode:
		var out []*yaml.Node
		seen := map[string]bool{}
		for _, m := range v.Content {
			if m.Kind != yaml.MappingNode {
				return nil, errMergeShape(val)
			}
			for i := 0; i+1 < len(m.Content); i += 2 {
				if !seen[m.Content[i].Value] {
					seen[m.Content[i].Value] = true
					out = append(out, m.Content[i], m.Content[i+1])
				}
			}
		}
		return out, nil
	}
	return nil, errMergeShape(val)
}

// errMergeShape is the refusal of a merge key whose value is no mapping.
func errMergeShape(val *yaml.Node) error {
	return fmt.Errorf("line %d: a merge key (<<) takes a mapping or a list of mappings", val.Line)
}

// anchorOnlyKey returns the first top-level key of payload that is not a
// runbook key and whose value carries an anchor: a key written only to hold
// an anchor, as Compose files do with x- keys.
func anchorOnlyKey(payload []byte) string {
	var doc yaml.Node
	if yaml.Unmarshal(payload, &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return ""
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if !RunbookKeys[root.Content[i].Value] && root.Content[i+1].Anchor != "" {
			return root.Content[i].Value
		}
	}
	return ""
}

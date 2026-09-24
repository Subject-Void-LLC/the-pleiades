// Package engine: the YAML view the strict key check (schema_keys.go)
// walks, seeing through documents, aliases and merge keys the way
// yaml.v3's own decoder does.
package engine

import "go.yaml.in/yaml/v3"

// maxAliasChain bounds how many aliases in a row the YAML view follows. A
// parsed document's aliases point at earlier anchors and cannot form a
// cycle, so this is a defensive ceiling, not a working limit.
const maxAliasChain = 64

// yamlKeyTree is a keyTree over a parsed yaml.Node. It sees through a
// document node and an alias to the node underneath, and treats a merge
// key's merged maps as though their keys were written in place, which is
// how yaml.v3 decodes them.
type yamlKeyTree struct {
	node *yaml.Node
}

// resolved follows a document node or an alias to the node it stands
// for, giving up (returning nil) past maxAliasChain hops.
func (t yamlKeyTree) resolved() *yaml.Node {
	n := t.node
	for hops := 0; n != nil; hops++ {
		switch {
		case hops > maxAliasChain:
			return nil
		case n.Kind == yaml.DocumentNode && len(n.Content) == 1:
			n = n.Content[0]
		case n.Kind == yaml.AliasNode:
			n = n.Alias
		default:
			return n
		}
	}
	return nil
}

// identity implements keyTree.
func (t yamlKeyTree) identity() any { return t.resolved() }

// mapping implements keyTree.
func (t yamlKeyTree) mapping() ([]keyEntry, bool) {
	n := t.resolved()
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, false
	}
	var entries []keyEntry
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if key.Kind == yaml.ScalarNode && key.ShortTag() == "!!merge" {
			entries = append(entries, mergedEntries(value)...)
			continue
		}
		entries = append(entries, keyEntry{key: key.Value, line: key.Line, value: yamlKeyTree{value}})
	}
	return entries, true
}

// mergedEntries returns the entries a merge key's value contributes: one
// map, or a list of maps, each possibly an alias.
func mergedEntries(value *yaml.Node) []keyEntry {
	source := yamlKeyTree{value}
	if entries, ok := source.mapping(); ok {
		return entries
	}
	var entries []keyEntry
	if items, ok := source.sequence(); ok {
		for _, item := range items {
			if more, ok := item.mapping(); ok {
				entries = append(entries, more...)
			}
		}
	}
	return entries
}

// sequence implements keyTree.
func (t yamlKeyTree) sequence() ([]keyTree, bool) {
	n := t.resolved()
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil, false
	}
	items := make([]keyTree, len(n.Content))
	for i, item := range n.Content {
		items[i] = yamlKeyTree{item}
	}
	return items, true
}

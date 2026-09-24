// Package playbook: reading a playbook into YAML nodes, within bounds.
package playbook

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"go.yaml.in/yaml/v3"
)

// errTooLarge marks a file over its size bound.
var errTooLarge = errors.New("file is larger than this converter reads")

// readBounded reads name from fsys, refusing a file over limit bytes. fsys
// is rooted at the playbook's directory (os.Root's FS in the CLI), so a
// name that escapes it, symlinks included, is refused by fsys itself.
func readBounded(fsys fs.FS, name string, limit int64) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: %w (%d bytes)", name, errTooLarge, limit)
	}
	return data, nil
}

// parseYAML parses data (from file) into its root node, after checking
// the document's size and depth with every alias expanded.
func parseYAML(data []byte, file string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid YAML: %w", file, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s is empty", file)
	}
	root := doc.Content[0]
	size, depth := measure(root)
	if size > maxExpandedNodes {
		return nil, fmt.Errorf("%s expands to more than %d YAML nodes with its aliases resolved", file, maxExpandedNodes)
	}
	if depth > maxYAMLDepth {
		return nil, fmt.Errorf("%s nests deeper than %d levels", file, maxYAMLDepth)
	}
	return root, nil
}

// measure returns n's size and depth with every alias expanded, visiting
// each node once: a memo per node makes an alias bomb cost its written
// size, not its expanded one.
func measure(n *yaml.Node) (size, depth int) {
	type result struct{ size, depth int }
	memo := map[*yaml.Node]result{}
	onPath := map[*yaml.Node]bool{}
	var walk func(*yaml.Node) result
	walk = func(n *yaml.Node) result {
		if n == nil {
			return result{}
		}
		if r, ok := memo[n]; ok {
			return r
		}
		if onPath[n] {
			// An alias cannot point at its own ancestor in a parsed
			// document, but a cycle must still end the walk.
			return result{size: maxExpandedNodes + 1}
		}
		onPath[n] = true
		defer delete(onPath, n)
		r := result{size: 1, depth: 1}
		children := n.Content
		if n.Kind == yaml.AliasNode {
			children = []*yaml.Node{n.Alias}
		}
		for _, c := range children {
			cr := walk(c)
			r.size += cr.size
			if r.size > maxExpandedNodes {
				r.size = maxExpandedNodes + 1
			}
			r.depth = max(r.depth, cr.depth+1)
		}
		memo[n] = r
		return r
	}
	r := walk(n)
	return r.size, r.depth
}

// deref follows aliases (and a document node) to the node they stand for.
// measure has already bounded the chain.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && (n.Kind == yaml.AliasNode || n.Kind == yaml.DocumentNode) {
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		} else if len(n.Content) == 1 {
			n = n.Content[0]
		} else {
			return n
		}
	}
	return n
}

// entry is one key of a YAML map, merged keys included.
type entry struct {
	key   string
	keyAt *yaml.Node
	value *yaml.Node
}

// mapEntries returns m's entries in order, with a merge key's maps
// expanded after them, and the keys m's own entries repeat. Precedence is
// PyYAML's, which Ansible parses with: an explicit key wins over a merged
// one and is not a repeat; of two merge keys in one map, the later wins;
// within one merge list, the earlier map wins.
func mapEntries(m *yaml.Node) (entries []entry, repeated []entry) {
	m = deref(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	explicit := map[string]bool{}
	var merges [][]entry
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.ShortTag() == "!!merge" {
			merges = append(merges, mergedEntries(v))
			continue
		}
		if explicit[k.Value] {
			repeated = append(repeated, entry{k.Value, k, v})
			continue
		}
		explicit[k.Value] = true
		entries = append(entries, entry{k.Value, k, v})
	}
	for i := len(merges) - 1; i >= 0; i-- {
		for _, e := range merges[i] {
			if !explicit[e.key] {
				explicit[e.key] = true
				entries = append(entries, e)
			}
		}
	}
	return entries, repeated
}

// mergedEntries returns what a merge key's value contributes.
func mergedEntries(v *yaml.Node) []entry {
	v = deref(v)
	if v == nil {
		return nil
	}
	switch v.Kind {
	case yaml.MappingNode:
		e, _ := mapEntries(v)
		return e
	case yaml.SequenceNode:
		var out []entry
		for _, item := range v.Content {
			e, _ := mapEntries(item)
			out = append(out, e...)
		}
		return out
	}
	return nil
}

// lookup returns the value under key in m, or nil.
func lookup(m *yaml.Node, key string) *yaml.Node {
	entries, _ := mapEntries(m)
	for _, e := range entries {
		if e.key == key {
			return e.value
		}
	}
	return nil
}

// isVault reports whether n is a vault-encrypted value.
func isVault(n *yaml.Node) bool {
	n = deref(n)
	return n != nil && n.Tag == "!vault"
}

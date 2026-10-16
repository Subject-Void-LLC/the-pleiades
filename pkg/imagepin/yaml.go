// Image references in Compose files and Kubernetes manifests.
package imagepin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// containerLists are the keys under which a pod's containers are listed,
// wherever they appear (a Pod, any workload's pod template, a CronJob's job
// template, or a custom resource embedding one).
var containerLists = map[string]bool{"containers": true, "initContainers": true, "ephemeralContainers": true}

// documents decodes every YAML document in content, refusing a mapping
// that gives a key twice.
func documents(content []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			if errors.Is(err, io.EOF) {
				return docs, nil
			}
			return nil, fmt.Errorf("not valid YAML: %w", err)
		}
		if err := uniqueKeys(&n); err != nil {
			return nil, err
		}
		docs = append(docs, &n)
	}
}

// uniqueKeys refuses a mapping anywhere under n that gives a key twice.
// yaml/v3's nodes keep both, and readers keep different ones (this file's
// first, a decoder into a map refuses), so the image checked could be the
// one that is not run; Compose refuses such a file, and so does kubectl's
// strict decoding. Aliases are not followed: what they name is checked
// where it is written.
func uniqueKeys(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			if seen[k.Value] {
				return fmt.Errorf("line %d: %q is given twice in one mapping", k.Line, k.Value)
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := uniqueKeys(c); err != nil {
			return err
		}
	}
	return nil
}

// detectYAML says whether content is a Compose file (a services mapping)
// or Kubernetes manifests (documents with apiVersion and kind).
func detectYAML(content []byte) (Kind, error) {
	docs, err := documents(content)
	if err != nil {
		return "", err
	}
	for _, d := range docs {
		root := body(d)
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		if child(root, "services") != nil {
			return KindCompose, nil
		}
		if child(root, "apiVersion") != nil && child(root, "kind") != nil {
			return KindKubernetes, nil
		}
	}
	return "", errors.New("it is YAML, but neither a Compose file (no services) nor Kubernetes manifests (no apiVersion and kind)")
}

// extractCompose finds each service's image.
func extractCompose(content []byte) ([]Ref, error) {
	docs, err := documents(content)
	if err != nil {
		return nil, err
	}
	lines := splitLines(content)
	var refs []Ref
	for _, d := range docs {
		services := child(body(d), "services")
		if services == nil || services.Kind != yaml.MappingNode {
			continue
		}
		for i := 1; i < len(services.Content); i += 2 {
			if image := child(services.Content[i], "image"); image != nil {
				refs = append(refs, scalarRef(content, lines, image, "image"))
			}
		}
	}
	return refs, nil
}

// extractKubernetes finds every container's image, in every document.
func extractKubernetes(content []byte) ([]Ref, error) {
	docs, err := documents(content)
	if err != nil {
		return nil, err
	}
	lines := splitLines(content)
	var refs []Ref
	for _, d := range docs {
		walk(body(d), func(key string, value *yaml.Node) {
			value = resolve(value)
			if !containerLists[key] || value == nil || value.Kind != yaml.SequenceNode {
				return
			}
			for _, c := range value.Content {
				if image := child(c, "image"); image != nil {
					refs = append(refs, scalarRef(content, lines, image, "image"))
				}
			}
		})
	}
	return refs, nil
}

// scalarRef is the reference a scalar node holds, located by its line and
// column and checked against the file, so a rewrite replaces exactly it.
// An anchored or tagged value's column is its anchor's or tag's, so those
// are stepped over exactly (never searched past: an anchor named like its
// value would be found first) before the value is matched.
func scalarRef(content []byte, lines []span, n *yaml.Node, where string) Ref {
	r := Ref{Line: n.Line, Text: n.Value, Where: where}
	switch {
	case n.Kind != yaml.ScalarNode:
		r.Unresolvable = "it is not a single value"
		return r
	case n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		r.Unresolvable = "it is written as a block scalar"
		return r
	case strings.Contains(n.Value, "${") || strings.HasPrefix(n.Value, "$"):
		r.Unresolvable = "it is a variable the file is interpolated with, so the reference is only known when it is"
	}
	if n.Line < 1 || n.Line > len(lines) {
		r.Unresolvable = "it could not be located in the file"
		return r
	}
	line := lines[n.Line-1]
	start := skipProperties(content, line.start+n.Column-1, line.end, n)
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		start++
	}
	end := start + len(n.Value)
	if start < line.start || end > line.end || string(content[start:end]) != n.Value {
		r.Unresolvable = "it could not be located in the file exactly as written (an escape in a quoted value, or a column before multi-byte text)"
		return r
	}
	r.Start, r.End = start, end
	return r
}

// skipProperties steps over a node's anchor (&name, exactly) and explicit
// tag (!tag), in either order, and the blanks after each, from at.
func skipProperties(content []byte, at, end int, n *yaml.Node) int {
	blanks := func(i int) int {
		for i < end && (content[i] == ' ' || content[i] == '\t') {
			i++
		}
		return i
	}
	for range 2 {
		switch {
		case n.Anchor != "" && bytes.HasPrefix(content[at:end], []byte("&"+n.Anchor)):
			at = blanks(at + 1 + len(n.Anchor))
		case n.Style&yaml.TaggedStyle != 0 && at < end && content[at] == '!':
			for at < end && content[at] != ' ' && content[at] != '\t' {
				at++
			}
			at = blanks(at)
		}
	}
	return at
}

// resolve follows an alias to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// maxMergeDepth bounds how many merge keys deep child looks, so merges
// that name each other cannot loop.
const maxMergeDepth = 16

// body is a document node's content, or the node itself.
func body(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// child is mapping n's value for key, or nil, as Compose and kubectl read
// it: through aliases, and through merge keys (<<: *anchor, or a list of
// them) when n does not give key itself, the first merged mapping that
// gives it winning, as YAML's merge key specifies.
func child(n *yaml.Node, key string) *yaml.Node {
	return childWithin(n, key, maxMergeDepth)
}

// childWithin is child, looking at most depth merge keys deep.
func childWithin(n *yaml.Node, key string, depth int) *yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode || depth < 0 {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; k.Value == key && k.Tag != "!!merge" {
			return resolve(n.Content[i+1])
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Tag != "!!merge" {
			continue
		}
		merged := resolve(n.Content[i+1])
		sources := []*yaml.Node{merged}
		if merged != nil && merged.Kind == yaml.SequenceNode {
			sources = merged.Content
		}
		for _, src := range sources {
			if v := childWithin(src, key, depth-1); v != nil {
				return v
			}
		}
	}
	return nil
}

// walk calls visit for every key and value in every mapping under n.
func walk(n *yaml.Node, visit func(key string, value *yaml.Node)) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			visit(n.Content[i].Value, n.Content[i+1])
		}
	}
	for _, c := range n.Content {
		walk(c, visit)
	}
}

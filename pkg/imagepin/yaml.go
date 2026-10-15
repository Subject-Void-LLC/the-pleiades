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

// documents decodes every YAML document in content.
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
		docs = append(docs, &n)
	}
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
	var refs []Ref
	for _, d := range docs {
		services := child(body(d), "services")
		if services == nil || services.Kind != yaml.MappingNode {
			continue
		}
		for i := 1; i < len(services.Content); i += 2 {
			if image := child(services.Content[i], "image"); image != nil {
				refs = append(refs, scalarRef(content, image, "image"))
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
	var refs []Ref
	for _, d := range docs {
		walk(body(d), func(key string, value *yaml.Node) {
			if !containerLists[key] || value.Kind != yaml.SequenceNode {
				return
			}
			for _, c := range value.Content {
				if image := child(c, "image"); image != nil {
					refs = append(refs, scalarRef(content, image, "image"))
				}
			}
		})
	}
	return refs, nil
}

// scalarRef is the reference a scalar node holds, located by its line and
// column and checked against the file, so a rewrite replaces exactly it.
func scalarRef(content []byte, n *yaml.Node, where string) Ref {
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
	lines := splitLines(content)
	if n.Line < 1 || n.Line > len(lines) {
		r.Unresolvable = "it could not be located in the file"
		return r
	}
	line := lines[n.Line-1]
	start := line.start + n.Column - 1
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

// body is a document node's content, or the node itself.
func body(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// child is mapping n's value for key, or nil.
func child(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
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

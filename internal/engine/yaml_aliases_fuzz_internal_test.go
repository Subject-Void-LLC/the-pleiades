// FuzzExpandAliases holds the alias expander to its contract on any YAML:
// either a refusal, or a tree with no alias node, anchor or merge key left,
// no deeper and no larger than its bounds, that decodes to what the
// document decoded to before (as a runbook's reader decodes it) and encodes
// to YAML that parses again.
package engine

import (
	"fmt"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

func FuzzExpandAliases(f *testing.F) {
	for _, seed := range []string{
		"a: &x {k: v}\nb: *x\n",
		"base: &b {name: one, port: 22}\ntask:\n  <<: *b\n  port: 2222\n",
		"m: &m1 {a: 1}\nn: &m2 {b: 2}\no: {<<: [*m1, *m2], c: 3}\n",
		"a: &a [x, x, x, x, x, x, x, x, x]\nb: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a]\nc: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b]\nd: [*c, *c, *c, *c, *c, *c, *c, *c, *c]\n",
		"x: &x [*x]\n",
		"a: {<<: *missing}\n",
		"a: {<<: {k: v}, <<: {j: w}}\n",
		"a: {<<: scalar}\n",
		"'<<': literal\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		var doc yaml.Node
		if yaml.Unmarshal([]byte(text), &doc) != nil {
			return
		}
		var before any
		decodedBefore := doc.Decode(&before) == nil
		if ExpandAliases(&doc) != nil {
			return
		}
		count := 0
		var walk func(n *yaml.Node, depth int)
		walk = func(n *yaml.Node, depth int) {
			count++
			switch {
			case n.Kind == yaml.AliasNode:
				t.Fatalf("an alias node survived expansion at line %d", n.Line)
			case n.Anchor != "":
				t.Fatalf("the anchor %q survived expansion at line %d", n.Anchor, n.Line)
			case depth > maxNodeDepth+1:
				t.Fatalf("the expanded tree nests %d deep, past %d", depth, maxNodeDepth)
			case count > maxExpandedNodes:
				t.Fatalf("the expanded tree holds more than %d nodes", maxExpandedNodes)
			}
			if n.Kind == yaml.MappingNode {
				merges := 0
				for i := 0; i+1 < len(n.Content); i += 2 {
					if k := n.Content[i]; k.Kind == yaml.ScalarNode && k.Value == mergeKey && k.Tag != "!!str" {
						merges++
					}
				}
				if merges > 0 {
					t.Fatalf("a merge key survived expansion at line %d", n.Line)
				}
			}
			for _, c := range n.Content {
				walk(c, depth+1)
			}
		}
		walk(&doc, 0)

		// What a runbook's reader does with it: decode it (task_syntax.go's
		// doc.Decode). A tree that decoded before expansion still decodes,
		// to the same value, keys compared as text: yaml.v3's own merge
		// decodes a merged key as a string ("0") where the expanded copy keeps
		// the type YAML gave it (0), which is the expander being right.
		var after any
		if err := doc.Decode(&after); decodedBefore && err != nil {
			t.Fatalf("the expanded tree no longer decodes: %v", err)
		} else if decodedBefore && !reflect.DeepEqual(keysAsText(before), keysAsText(after)) {
			t.Fatalf("expansion changed what the document decodes to:\n%#v\n%#v", before, after)
		}
		// And it encodes to YAML that parses again.
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return // a tree yaml.v3 itself cannot encode (a tag it refuses); not this function's
		}
		var again yaml.Node
		if err := yaml.Unmarshal(out, &again); err != nil {
			t.Fatalf("the expanded tree encodes to YAML that does not parse: %v\n%s", err, out)
		}
	})
}

// keysAsText returns v with every mapping's keys written as text, so two
// decodings that differ only in a key's Go type compare equal.
func keysAsText(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = keysAsText(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(k)] = keysAsText(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = keysAsText(e)
		}
		return out
	}
	return v
}

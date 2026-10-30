// Package engine: hosts: written as a list. A runbook's hosts: is one string
// naming a device or a tag; a YAML list of them is joined into a
// comma-separated string at parse time (joinHostsList), and ResolveTargets
// resolves that string as the union of its names. The string form needs no
// other representation, so the DAG, the journal and a dispatch carry it as
// they always have.
package engine

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// hostsSeparator joins a hosts: list into the one string the DAG holds, and
// splits it again to resolve. It is Ansible's own spelling of a host union
// (hosts: a,b), so a runbook written either way means the same.
const hostsSeparator = ","

// TargetResolverFunc adapts a function to TargetResolver, for a caller that
// resolves one name its own way and wants ResolveTargets' union over it.
type TargetResolverFunc func(target string) []inventory.InventoryItem

// Resolve calls f.
func (f TargetResolverFunc) Resolve(target string) []inventory.InventoryItem { return f(target) }

// ResolveTargets resolves target as a task's hosts or params.target: the
// devices it names, and the names in it that matched nothing.
//
// The whole string is asked first, exactly as before lists existed, so a device
// or tag whose name holds a comma still resolves as itself. Only when the
// whole string matches nothing and holds a comma is it read as a list, and the
// result is the union of each name, each device once, in the order first
// named. A name in a list that matches nothing is returned in missing and is
// never dropped without a word: a run that reached fewer devices than its
// runbook names would be a quiet partial change (FAILURE_PATTERNS 11).
//
// An empty target resolves through r as it always has (a resolver may carry an
// ambient default device) and reports nothing missing.
func ResolveTargets(r TargetResolver, target string) (devices []inventory.InventoryItem, missing []string) {
	whole := r.Resolve(target)
	if len(whole) > 0 || target == "" {
		return whole, nil
	}
	names := splitHosts(target)
	if len(names) < 2 {
		return nil, []string{target}
	}
	seen := map[inventory.DeviceID]bool{}
	for _, name := range names {
		found := r.Resolve(name)
		if len(found) == 0 {
			missing = append(missing, name)
			continue
		}
		for _, d := range found {
			if !seen[d.ID()] {
				seen[d.ID()] = true
				devices = append(devices, d)
			}
		}
	}
	return devices, missing
}

// splitHosts returns the non-empty, trimmed names of a hosts string that holds
// a separator, or nil when it holds none.
func splitHosts(target string) []string {
	if !strings.Contains(target, hostsSeparator) {
		return nil
	}
	var names []string
	for _, part := range strings.Split(target, hostsSeparator) {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// joinHostsList rewrites a hosts: sequence node into the scalar the DAG holds.
// Each item must be a non-empty plain name; a mapping, a nested list, an empty
// item or an item holding the separator is refused with the line it is on, and
// so is an empty list, which would fall back to running nothing named.
func joinHostsList(seq *yaml.Node) error {
	if len(seq.Content) == 0 {
		return fmt.Errorf("line %d: hosts: is an empty list, name at least one device or tag", seq.Line)
	}
	names := make([]string, 0, len(seq.Content))
	for i, item := range seq.Content {
		name := strings.TrimSpace(item.Value)
		switch {
		case item.Kind != yaml.ScalarNode:
			return fmt.Errorf("line %d: hosts: item %d is a %s, want a device or tag name", item.Line, i+1, nodeKindName(item))
		case name == "":
			return fmt.Errorf("line %d: hosts: item %d is empty, want a device or tag name", item.Line, i+1)
		case strings.Contains(name, hostsSeparator):
			return fmt.Errorf("line %d: hosts: item %d (%q) holds a comma, which separates names; write one name per item", item.Line, i+1, name)
		}
		names = append(names, name)
	}
	*seq = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Join(names, hostsSeparator), Line: seq.Line, Column: seq.Column}
	return nil
}

// nodeKindName is a YAML node's kind in words, for an error message.
func nodeKindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "list"
	case yaml.AliasNode:
		return "alias"
	default:
		return "value"
	}
}

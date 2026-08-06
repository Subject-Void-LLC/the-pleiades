package inventory

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// mergeHostsIntoDocument produces the YAML document node that EncodeHosts
// serializes. original is the previous on-disk content (nil for a fresh
// file); hosts is the complete desired set of hosts after whatever change
// the caller is making (one appended host for add-host, one mutated
// Properties bag for Save).
//
// This is the fix for the chain audit's "add-host destroys inventory
// content it does not understand" finding: every top-level key besides
// "hosts" (a hand-written "groups:" or "vars:" block), every per-host key
// HostSpec does not model, and every comment, all live on unchanged nodes
// this function never touches. Only the "hosts" sequence, and only the
// fields HostSpec actually carries within each entry, are rewritten.
func mergeHostsIntoDocument(original []byte, hosts []HostSpec) (*yaml.Node, error) {
	root, err := documentRoot(original)
	if err != nil {
		return nil, err
	}

	hostsSeq := findOrCreateChild(root, "hosts", yaml.SequenceNode, "!!seq")

	existingByIdentity := make(map[string]*yaml.Node, len(hostsSeq.Content))
	for _, entry := range hostsSeq.Content {
		if id := entryIdentity(entry); id != "" {
			existingByIdentity[id] = entry
		}
	}

	merged := make([]*yaml.Node, 0, len(hosts))
	for _, h := range hosts {
		entry, ok := existingByIdentity[hostIdentity(h)]
		if !ok {
			entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		if err := applyHostSpec(entry, h); err != nil {
			return nil, fmt.Errorf("failed to encode host %q: %w", h.Name, err)
		}
		merged = append(merged, entry)
	}
	hostsSeq.Content = merged

	// An empty sequence parses as flow style ("hosts: []"), which is what
	// `pleiades init` scaffolds and what yaml.Marshal then preserves. Left
	// alone, the first write that actually adds hosts emits every one of
	// them inline on a single line, so a project synced from a controller
	// ends up with a multi-thousand-character line instead of the
	// hand-editable file Section 7 promises. Once the sequence has entries,
	// block style is the only readable choice; a sequence a human
	// deliberately wrote in flow style is already non-empty and is left as
	// it is.
	if len(merged) > 0 && hostsSeq.Style == yaml.FlowStyle {
		hostsSeq.Style = 0
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return doc, nil
}

// documentRoot parses original into its top-level mapping node, or builds
// an empty one if original has no content yet (a brand new inventory
// file). An original that parses but is not a mapping at the top level
// (a bare YAML list, for instance) is rejected rather than silently
// discarded, since silently discarding is exactly the defect this file
// exists to fix.
func documentRoot(original []byte) (*yaml.Node, error) {
	if len(original) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse existing inventory YAML: %w", err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("inventory file does not contain a YAML mapping at the top level")
	}
	return root, nil
}

// hostIdentity is the key hosts are matched by across a merge: ID when
// set, Name otherwise. This mirrors findHostIndex's (file_repository_save.go)
// identical fallback, since both answer the same question, "which on-disk
// entry is this HostSpec," and must agree or a host could be matched by
// one and silently duplicated by the other.
func hostIdentity(h HostSpec) string {
	if h.ID != "" {
		return h.ID
	}
	return h.Name
}

// entryIdentity is hostIdentity's counterpart for a raw mapping node read
// from disk, used to find which existing entry a HostSpec corresponds to.
func entryIdentity(entry *yaml.Node) string {
	if id := mappingValue(entry, "id"); id != "" {
		return id
	}
	return mappingValue(entry, "name")
}

// applyHostSpec sets the YAML keys HostSpec models (id, name, type,
// classify, tags, properties) on dst, in place. Any other key already on
// dst, and any comment attached to dst or to a key/value node dst keeps,
// is untouched. id, name, and type always end up present, matching
// HostSpec's own non-omitempty tags; classify, tags, and properties are
// removed entirely when empty, matching their omitempty tags, so a
// cleared field does not linger as stale content.
func applyHostSpec(dst *yaml.Node, h HostSpec) error {
	if dst.Kind == 0 {
		dst.Kind = yaml.MappingNode
		dst.Tag = "!!map"
	}
	setScalar(dst, "id", h.ID)
	setScalar(dst, "name", h.Name)
	setScalar(dst, "type", h.Type)
	if err := setOrRemove(dst, "classify", h.Classify, len(h.Classify) == 0); err != nil {
		return err
	}
	if err := setOrRemove(dst, "tags", h.Tags, len(h.Tags) == 0); err != nil {
		return err
	}
	if err := setOrRemove(dst, "properties", h.Properties, len(h.Properties) == 0); err != nil {
		return err
	}
	return nil
}

// mappingValue returns the plain scalar value of key within m, or "" if m
// is not a mapping or has no such key.
func mappingValue(m *yaml.Node, key string) string {
	if m.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1].Value
		}
	}
	return ""
}

// findOrCreateChild returns m's existing child node for key, or appends a
// new one of the given kind/tag if key is not present yet.
func findOrCreateChild(m *yaml.Node, key string, kind yaml.Kind, tag string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	child := &yaml.Node{Kind: kind, Tag: tag}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	return child
}

// setScalar sets key to value on m, creating the key if absent, without
// disturbing any other key.
func setScalar(m *yaml.Node, key, value string) {
	findOrCreateChild(m, key, yaml.ScalarNode, "!!str").SetString(value)
}

// setOrRemove sets key to the encoded form of value on m, or removes key
// entirely from m when empty is true, so an omitempty field's absence is
// represented the same way whether the entry is freshly created or being
// updated in place.
func setOrRemove(m *yaml.Node, key string, value interface{}, empty bool) error {
	if empty {
		removeChild(m, key)
		return nil
	}
	// The placeholder kind/tag passed to findOrCreateChild is irrelevant:
	// the Encode below overwrites the node's Kind/Tag/Content wholesale
	// with whatever value's real shape is (a sequence for []string, a
	// mapping for map[string]interface{}), it only needs a *yaml.Node to
	// exist at the right position in m.Content to overwrite in place.
	child := findOrCreateChild(m, key, yaml.ScalarNode, "!!null")
	fresh := &yaml.Node{}
	if err := fresh.Encode(value); err != nil {
		return fmt.Errorf("failed to encode %q: %w", key, err)
	}
	*child = *fresh
	return nil
}

// removeChild deletes key from m if present; a no-op otherwise.
func removeChild(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

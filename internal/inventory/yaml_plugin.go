package inventory

import (
	"fmt"
	"os"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"go.yaml.in/yaml/v3"
)

// HostSpec is the YAML-facing shape of one static inventory entry. It
// carries an explicit Type, since PLAN.md Section 6a treats StaticYAMLPlugin
// as the simple case with no Connect/Discover pass: the file already says
// what the host is. It also carries an explicit ID, since DeviceID must
// never be the mutable Name; add-host always writes one.
//
// Classify is the Section 6d alternative to Type: a classification path
// (e.g. ["linux_server", "debian_family", "ubuntu"]) resolved against
// classification.DefaultRuleSet by ResolveHostType. Type always wins when
// both are present; add-host itself resolves Classify into a concrete Type
// eagerly at write time and persists both, so Classify's presence on an
// already-written entry is provenance, not something re-resolved on every
// load. A hand-written entry carrying only Classify (no Type) still
// resolves correctly at hydration time, since both hydration paths
// (fileRepository.buildRecord, HydrateHosts) call ResolveHostType rather
// than reading Type directly.
type HostSpec struct {
	ID         string                 `yaml:"id"`
	Name       string                 `yaml:"name"`
	Type       string                 `yaml:"type"`
	Classify   []string               `yaml:"classify,omitempty"`
	Tags       []string               `yaml:"tags,omitempty"`
	Properties map[string]interface{} `yaml:"properties,omitempty"`
}

// yamlInventoryFile is the on-disk document shape: a flat list of hosts,
// per Section 7's "just list hosts" Walk-tier promise.
type yamlInventoryFile struct {
	Hosts []HostSpec `yaml:"hosts"`
}

// ParseHosts decodes a static YAML inventory document into HostSpecs. It
// takes bytes rather than a path so it can be fuzzed without touching a
// filesystem; ReadHosts is the file-backed convenience wrapper.
func ParseHosts(data []byte) ([]HostSpec, error) {
	var doc yamlInventoryFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inventory YAML: %w", err)
	}
	return doc.Hosts, nil
}

// ReadHosts reads and parses a static YAML inventory file.
func ReadHosts(path string) ([]HostSpec, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is the CLI's own --dir/project path, not untrusted input; see cmd/pleiades's W1 Schema/Injection Hardening item
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory file %s: %w", path, err)
	}
	return ParseHosts(data)
}

// EncodeHosts serializes hosts back to a YAML document, merged into
// original (the file's previous content, or nil for a brand new file)
// rather than built from hosts alone. Merging, not replacing, is what
// stops a write from destroying content HostSpec cannot represent: a
// hand-written top-level "groups:" or "vars:" block, a per-host key no
// HostSpec field maps to, and comments anywhere in the file all live on
// document nodes this function never touches, since only the "hosts"
// sequence, and only the fields HostSpec actually carries, are rewritten
// (see mergeHostsIntoDocument, yaml_merge.go). Round-tripping through
// ParseHosts/EncodeHosts with the parsed content as original is therefore
// lossless for content HostSpec models and preserving for content it does
// not, not merely lossless for the data model alone as an earlier version
// of this comment claimed.
func EncodeHosts(original []byte, hosts []HostSpec) ([]byte, error) {
	doc, err := mergeHostsIntoDocument(original, hosts)
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inventory YAML: %w", err)
	}
	return data, nil
}

// WriteHosts serializes hosts and writes them to a static YAML inventory
// file, merging into whatever the file already contains (see EncodeHosts)
// rather than overwriting it wholesale. A file that does not exist yet is
// treated as an empty original, producing a fresh document exactly as
// before.
func WriteHosts(path string, hosts []HostSpec) error {
	original, err := os.ReadFile(path) // #nosec G304 -- same CLI-owned path as ReadHosts above
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read existing inventory file %s: %w", path, err)
		}
		original = nil
	}
	data, err := EncodeHosts(original, hosts)
	if err != nil {
		return err
	}
	// 0o644, not 0o600: hosts.yaml is the hand-editable inventory file
	// PLAN.md Section 7 promises, meant to be read (and often shared or
	// version-controlled) like any other project file, not a secret; see
	// the identical reasoning on project.go's scaffolded files.
	if err := os.WriteFile(path, data, 0o644); err != nil { // #nosec G306 G703 -- intentional permissions (comment above); path is the CLI's own --dir/project path, not untrusted input, same as ReadHosts above
		return fmt.Errorf("failed to write inventory file %s: %w", path, err)
	}
	return nil
}

// HydrateHosts builds an InventoryItem for each HostSpec through factory,
// the same one the ent-backed repository hydrates through, so a static
// YAML inventory and a database-backed one produce identical concrete
// types for identical device data. It resolves each host's device type
// through ResolveHostType against classification.DefaultRuleSet, so a
// hand-written entry carrying only Classify (no Type) hydrates the same
// concrete type fileRepository.buildRecord would give it.
func HydrateHosts(factory *ItemFactory, hosts []HostSpec) ([]inventory.InventoryItem, error) {
	ruleSet := classification.DefaultRuleSet()
	items := make([]inventory.InventoryItem, 0, len(hosts))
	for _, h := range hosts {
		id := h.ID
		if id == "" {
			// A hand-edited file may omit id. Falling back to Name keeps
			// this run working, but a later edit that also renames the
			// host will not be recognized as the same device: add-host
			// always writes a real id so this path is a fallback, not the
			// normal one.
			id = h.Name
		}

		deviceType, err := ResolveHostType(h, ruleSet)
		if err != nil {
			return nil, fmt.Errorf("failed to hydrate host %q: %w", h.Name, err)
		}

		caps, err := ResolveHostCapabilities(h, ruleSet)
		if err != nil {
			return nil, fmt.Errorf("failed to hydrate host %q: %w", h.Name, err)
		}

		rec := record.Record{
			ID:         inventory.DeviceID(id),
			Name:       h.Name,
			Type:       deviceType,
			Properties: h.Properties,
			Tags:       toTags(h.Tags),
			// Walk tier has no onboarding pipeline (Section 6b is
			// Crawl-tier): a host listed in the file is immediately active.
			State:        inventory.StateActive,
			Source:       inventory.SourceAuthority{Plugin: "static_yaml"},
			Capabilities: caps,
		}

		item, err := factory.Build(rec)
		if err != nil {
			return nil, fmt.Errorf("failed to hydrate host %q: %w", h.Name, err)
		}
		items = append(items, item)
	}
	return items, nil
}

func toTags(ss []string) []inventory.Tag {
	tags := make([]inventory.Tag, len(ss))
	for i, s := range ss {
		tags[i] = inventory.Tag(s)
	}
	return tags
}

// tagsToStrings is toTags's inverse, for a write path (entRepository.Save)
// handing an item's Tags back to a backend that stores plain strings.
func tagsToStrings(tags []inventory.Tag) []string {
	ss := make([]string, len(tags))
	for i, t := range tags {
		ss[i] = string(t)
	}
	return ss
}

// StaticYAMLPlugin hydrates a static YAML inventory file into InventoryItems.
// It is PLAN.md Section 6a's "simple case" SyncPlugin: unlike a NetBox or
// AWS plugin, it has no external system to Connect to and no ambiguous
// data that needs a Discover/Classify pass, so it does not implement the
// full four-method SyncPlugin interface (Connect/Discover/Classify/Sync).
// That interface's supporting types (Classification, Reconciliation,
// PluginConfig) belong to Phase 6, which owns dynamic sync plugins with a
// real discovery pipeline; inventing them here for one static-file
// implementation would be the premature generalization Gate 1's
// reusability check exists to catch. StaticYAMLPlugin instead produces the
// same Records the ent-backed repository does, which is the part Walk
// tier actually needs.
type StaticYAMLPlugin struct {
	path    string
	factory *ItemFactory
}

// NewStaticYAMLPlugin creates a plugin reading from the given file path.
func NewStaticYAMLPlugin(path string, factory *ItemFactory) *StaticYAMLPlugin {
	return &StaticYAMLPlugin{path: path, factory: factory}
}

// Load reads and hydrates every host in the inventory file.
func (p *StaticYAMLPlugin) Load() ([]inventory.InventoryItem, error) {
	hosts, err := ReadHosts(p.path)
	if err != nil {
		return nil, err
	}
	return HydrateHosts(p.factory, hosts)
}

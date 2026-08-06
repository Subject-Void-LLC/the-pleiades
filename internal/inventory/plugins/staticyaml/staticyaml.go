// Package staticyaml implements the PLAN.md Section 6a sync plugin for a
// static YAML inventory file: the simple case, where the document already
// says what each host is.
//
// It exists in the plugin registry alongside the network-backed plugins for
// a reason beyond tidiness. It is the control case that keeps the
// syncplugin.Plugin port honest: it has no network, no authentication, no
// paging, and no ambiguity to resolve, so any part of the port it cannot
// satisfy naturally is a part shaped around some other plugin's specifics.
// The conformance suite runs it and the Catalyst Center plugin through
// identical assertions for exactly that reason.
package staticyaml

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
	inv "github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// Name is this plugin's registration key and the value stamped into the
// SourceAuthority of every device it syncs.
const Name = "static_yaml"

// init registers this plugin. It only runs if something imports this
// package, which is what internal/inventory/plugins/builtins.go exists to
// guarantee (FAILURE_PATTERNS.md #52: a registration that nothing imports
// is a registration that never happens in the real binary).
func init() {
	syncplugin.MustRegister(syncplugin.Descriptor{
		Name:        Name,
		Description: "reads a static hosts.yaml inventory file",
		DefaultConfig: syncplugin.Config{
			Name: Name,
			// A local file is not a remote source of truth this platform
			// must avoid writing back to: hosts.yaml is the hand-editable
			// file Walk tier promises, and add-host already writes it.
			ReadOnly: false,
		},
		// This plugin's four methods do real work against a real file, and
		// its tests exercise them through a real Repository. Its upstream
		// system is the local filesystem, so "proven against the real
		// upstream" needs no network and no sandbox.
		Status: syncplugin.StatusImplemented,
		New:    func() syncplugin.Plugin { return New(inv.NewItemFactory()) },
	})
}

// Plugin reads hosts from a static YAML document. The zero value is not
// usable; construct one with New.
type Plugin struct {
	factory *inv.ItemFactory
	ruleSet *classification.RuleSet

	// path is resolved from Config.Endpoint at Connect time. An empty path
	// means Connect has not run, which is what makes ErrNotConnected
	// detectable rather than a nil-pointer panic later.
	path string
}

// compile-time proof this plugin satisfies the port. Without it, a drift
// between the interface and this implementation would only surface at the
// registry call site.
var _ syncplugin.Plugin = (*Plugin)(nil)

// New creates an unconnected plugin hydrating through factory.
func New(factory *inv.ItemFactory) *Plugin {
	return &Plugin{factory: factory, ruleSet: classification.DefaultRuleSet()}
}

// Connect resolves the inventory file path from cfg. There is nothing to
// authenticate against, so this is the whole of it: the method exists
// because the port's call order guarantees it runs before Discover, which
// makes it the right place to fail on a missing or malformed locator.
func (p *Plugin) Connect(_ context.Context, cfg syncplugin.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	path, ok := cfg.FilePath()
	if !ok {
		return fmt.Errorf("sync plugin %q: endpoint must be a file:// URL, got %q", Name, cfg.Endpoint)
	}
	p.path = path
	return nil
}

// Discover reads the document and streams one Record per host. Unlike a
// network-backed plugin it reads everything up front, because the file is
// already fully in memory the moment it is parsed and pretending to page
// through it would be a fiction that hides nothing.
func (p *Plugin) Discover(_ context.Context) (syncplugin.RecordIterator, error) {
	if p.path == "" {
		return nil, syncplugin.ErrNotConnected
	}

	hosts, err := inv.ReadHosts(p.path)
	if err != nil {
		return nil, err
	}

	records := make([]record.Record, 0, len(hosts))
	for _, h := range hosts {
		deviceType, err := inv.ResolveHostType(h, p.ruleSet)
		if err != nil {
			return nil, err
		}
		caps, err := inv.ResolveHostCapabilities(h, p.ruleSet)
		if err != nil {
			return nil, err
		}

		id := h.ID
		if id == "" {
			// A hand-edited file may omit id. Falling back to Name keeps
			// this sync working, matching HydrateHosts' identical
			// fallback, though a later rename will not be recognized as
			// the same device.
			id = h.Name
		}

		records = append(records, record.Record{
			ID:           inventory.DeviceID(id),
			Name:         h.Name,
			Type:         deviceType,
			Properties:   h.Properties,
			Tags:         toTags(h.Tags),
			Capabilities: caps,
		})
	}

	return &sliceIterator{records: records, pos: -1}, nil
}

// Classify echoes back the type the document already stated, rather than
// walking the classification rule tree. That is the whole difference
// between this plugin and a discovery-based one: the file is the
// classification, so re-deriving it would be inventing an opinion the user
// did not ask for. A host with no resolvable type is quarantined, which is
// the one case where the file failed to answer the question.
func (p *Plugin) Classify(_ context.Context, rec record.Record) (syncplugin.Classification, error) {
	if rec.Type == "" {
		return syncplugin.Quarantine("inventory file entry has neither a type nor a resolvable classify path"), nil
	}
	return syncplugin.Classification{
		Type:         rec.Type,
		Capabilities: rec.Capabilities,
		// Walk tier has no onboarding pipeline, so a host listed in the
		// file is immediately active. This matches HydrateHosts' own
		// long-standing default rather than introducing a second answer.
		State: inventory.StateActive,
	}, nil
}

// Sync reconciles the document into repo through the shared driver.
func (p *Plugin) Sync(ctx context.Context, repo inv.Repository) (syncplugin.Reconciliation, error) {
	cfg := syncplugin.Config{Name: Name}
	return syncplugin.Reconcile(ctx, p, cfg, repo, p.factory)
}

// Close releases nothing. This plugin holds no connection and no handle:
// Discover reads the whole document and closes the file before returning.
// The method exists to satisfy the port, which cannot know that.
func (p *Plugin) Close() error { return nil }

// toTags converts the document's plain strings into domain Tags.
func toTags(ss []string) []inventory.Tag {
	tags := make([]inventory.Tag, len(ss))
	for i, s := range ss {
		tags[i] = inventory.Tag(s)
	}
	return tags
}

// sliceIterator streams an already-materialized slice of Records. It starts
// at -1 so the first Next lands on index 0, matching every other iterator
// in this codebase where Next advances before Item is read.
type sliceIterator struct {
	records []record.Record
	pos     int
}

// Next advances to the next Record, reporting whether one is available.
func (it *sliceIterator) Next(_ context.Context) bool {
	if it.pos+1 >= len(it.records) {
		return false
	}
	it.pos++
	return true
}

// Record returns the Record at the cursor.
func (it *sliceIterator) Record() record.Record {
	if it.pos < 0 || it.pos >= len(it.records) {
		return record.Record{}
	}
	return it.records[it.pos]
}

// Error always returns nil: the whole document was read and parsed before
// this iterator was constructed, so there is no later failure to report.
func (it *sliceIterator) Error() error { return nil }

// Close releases nothing, because a materialized slice holds no cursor or
// file handle. It exists to satisfy the port, which cannot know that.
func (it *sliceIterator) Close() error { return nil }

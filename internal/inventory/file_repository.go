package inventory

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// fileRepository is a Repository backed by a pair of YAML files:
// hosts.yaml (HostSpec's existing, hand-editable current-state row, see
// yaml_plugin.go) and a sidecar file this package owns exclusively,
// carrying the optimistic-concurrency version, lifecycle state, and audit
// trail HostSpec deliberately does not. See sidecarEntry's doc comment in
// file_repository_state.go for why the split exists: it applies the same
// reasoning HANDOFF_DOCUMENT.md's "the write path is now BUILT" section
// gives for the ent schema's separate Revision table ("the questions it
// answers are cross-device and cross-time and a blob cannot be indexed for
// those") to a file pair instead of a database. This is the Walk-tier
// adapter behind the same Repository port entRepository implements at
// Crawl and above; both must behave identically to callers.
type fileRepository struct {
	hostsPath   string // path to the primary, hand-editable hosts.yaml
	sidecarPath string // path to the generated version/history sidecar
	factory     *ItemFactory
	ruleSet     *classification.RuleSet // built once; see ResolveHostType

	// mu serializes Save calls within this process. Save's correctness
	// does not come from the mutex (the re-read-and-compare of the stored
	// version does that); the mutex exists so two goroutines calling Save
	// concurrently through the same *fileRepository never interleave their
	// own read and write of the two files on disk, which the re-read
	// check alone cannot prevent.
	mu sync.Mutex
}

// NewFileRepository creates a Repository backed by the YAML inventory file
// at inventoryPath. It derives the sidecar file's path automatically: the
// sidecar always lives in the same directory as inventoryPath, under the
// fixed name sidecarFileName, so no caller has to pass or remember a
// second path.
func NewFileRepository(inventoryPath string, factory *ItemFactory) Repository {
	dir := filepath.Dir(inventoryPath)
	return &fileRepository{
		hostsPath:   inventoryPath,
		sidecarPath: filepath.Join(dir, sidecarFileName),
		factory:     factory,
		ruleSet:     classification.DefaultRuleSet(),
	}
}

// GetGroup returns an Iterator over every host in the inventory file. Walk
// tier has no real grouping infrastructure yet anywhere in this codebase:
// HostSpec (yaml_plugin.go) has no group-membership field at all, unlike
// the ent-backed adapter, which now pushes sel.GroupName down to SQL via a
// real Group edge (ent_repository.go). Honoring sel.GroupName here would
// mean inventing filtering infrastructure that exists nowhere else in the
// platform yet, so this does the same honest thing rather than
// fake-supporting a Selector field it cannot act on:
// TestFileRepository_Selector_GroupNameIgnored pins this down so a future
// change cannot silently start erroring on it instead.
//
// Items it yields carry Version but not History, matching the Repository
// interface's documented list-view contract (iterator.go).
func (r *fileRepository) GetGroup(ctx context.Context, sel inventory.Selector) (Iterator, error) {
	hosts, err := ReadHosts(r.hostsPath)
	if err != nil {
		return nil, err
	}
	sidecar, err := readSidecar(r.sidecarPath)
	if err != nil {
		return nil, err
	}

	items := make([]inventory.InventoryItem, 0, len(hosts))
	for _, h := range hosts {
		// withHistory=false: a list view does not display history, and
		// loading it for every row would be work for data never shown.
		rec, err := r.buildRecord(h, sidecar, false)
		if err != nil {
			return nil, err
		}
		item, err := r.factory.Build(rec)
		if err != nil {
			return nil, fmt.Errorf("factory failed to build item %s: %w", h.Name, err)
		}
		items = append(items, item)
	}

	return &fileIterator{items: items}, nil
}

// GetByName returns a single host by its unique name, together with its
// full audit trail loaded from the sidecar. This is the read counterpart
// to Save: it is how a caller reloads after ErrVersionConflict, and how
// anything that needs History rather than just current state fetches an
// item.
func (r *fileRepository) GetByName(ctx context.Context, name string) (inventory.InventoryItem, error) {
	hosts, err := ReadHosts(r.hostsPath)
	if err != nil {
		return nil, err
	}

	// Find by Name, since that is what callers address a host by. The
	// sidecar itself is still correlated by ID (see findSidecarEntry):
	// this lookup just resolves the caller's Name into the HostSpec row,
	// which carries the ID the sidecar is actually keyed on.
	var found *HostSpec
	for i := range hosts {
		if hosts[i].Name == name {
			found = &hosts[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("host %s: %w", name, ErrItemNotFound)
	}

	sidecar, err := readSidecar(r.sidecarPath)
	if err != nil {
		return nil, err
	}

	rec, err := r.buildRecord(*found, sidecar, true) // true: full read, load history
	if err != nil {
		return nil, err
	}

	item, err := r.factory.Build(rec)
	if err != nil {
		return nil, fmt.Errorf("factory failed to build item %s: %w", name, err)
	}
	return item, nil
}

// buildRecord converts one HostSpec plus its (possibly absent) sidecar
// entry into the storage-agnostic Record the factory hydrates. A host with
// no sidecar entry yet has never been through Save, so it defaults to
// version 0 and StateActive, the same default HydrateHosts (yaml_plugin.go)
// uses for the read-only StaticYAMLPlugin path: "a host listed in the file
// is immediately active." withHistory controls whether the sidecar's
// stored audit trail is attached, so GetGroup's list view and GetByName's
// full read share this one conversion instead of two copies of it.
func (r *fileRepository) buildRecord(h HostSpec, sidecar sidecarDocument, withHistory bool) (record.Record, error) {
	id := h.ID
	if id == "" {
		// A hand-edited file may omit id. Falling back to Name keeps this
		// run working, but see HydrateHosts's identical fallback in
		// yaml_plugin.go for why this is a fallback, not the normal path:
		// add-host always writes a real id.
		id = h.Name
	}
	deviceID := inventory.DeviceID(id)

	deviceType, err := ResolveHostType(h, r.ruleSet)
	if err != nil {
		return record.Record{}, err
	}

	caps, err := ResolveHostCapabilities(h, r.ruleSet)
	if err != nil {
		return record.Record{}, err
	}

	rec := record.Record{
		ID:   deviceID,
		Name: h.Name,
		Type: deviceType,
		// PropertyValue is a type alias for any (pkg/inventory/item.go),
		// so HostSpec's map[string]interface{} and Record's
		// map[string]PropertyValue are the same type; no conversion needed.
		Properties: h.Properties,
		Tags:       toTags(h.Tags), // toTags is unexported in yaml_plugin.go, same package
		State:      inventory.StateActive,
		// Source is deliberately left zero when the sidecar recorded none.
		// It used to default to Plugin: "file", which conflated two
		// different questions: where the data is stored, and which sync
		// plugin authoritatively owns it. Section 11's One Authority Per
		// Item is about the second. Naming the storage backend as the owner
		// made every hand-written hosts.yaml entry look like it was already
		// claimed by a plugin called "file", so the first real sync plugin
		// to run against a Walk-tier project reported every host as a
		// conflict and refused to adopt any of them. An empty Plugin is the
		// honest answer for "provenance was never recorded", and it is the
		// value reconciliation already treats as adoptable.
		Capabilities: caps,
	}

	entry, idx := findSidecarEntry(sidecar, deviceID)
	if idx == -1 {
		// Never saved: the defaults above (version 0, StateActive, no
		// history) stand as-is.
		return rec, nil
	}

	rec.Version = entry.Version
	if entry.Source != "" {
		// A sidecar entry that recorded provenance overrides the "file"
		// default: some later session's Save call is what actually wrote
		// this entry, and its own Source at the time is worth keeping
		// rather than discarding in favor of the generic default.
		rec.Source.Plugin = entry.Source
		if entry.SourceSyncedAt != nil {
			rec.Source.SyncedAt = *entry.SourceSyncedAt
		}
	}
	if entry.State != "" {
		// An unrecognized stored state is an error, never a silent
		// promotion to active, mirroring entRepository.toRecord
		// (ent_repository.go): StateActive is the one state that permits
		// execution, so guessing it for a value this build does not
		// understand would let a quarantined or archived device accept
		// work.
		state, err := inventory.ParseLifecycleState(entry.State)
		if err != nil {
			return record.Record{}, fmt.Errorf("host %s: %w", h.Name, err)
		}
		rec.State = state
	}
	if withHistory {
		rec.History = toRevisions(entry.History)
	}
	return rec, nil
}

// Save is implemented in file_repository_save.go, alongside its write-path
// helpers (findHostIndex, writeHostsAtomic, atomicWriteFile), to keep this
// file's line count within the ~300-line soft cap. See that file's Save
// doc comment for the full optimistic-concurrency contract.

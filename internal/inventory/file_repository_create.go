package inventory

import (
	"context"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Create appends a new host to the inventory file and its sidecar entry to
// the state file. It is the file-backed half of the Repository write path,
// and it is what lets a Crawl-tier user run a sync plugin with no database
// at all, which Section 7's binding rule 2 requires of every port the
// execution path touches.
//
// The write ordering is the mirror image of Save's, and deliberately so.
// Save writes the sidecar first, because a version that runs ahead of the
// data is safe while the reverse silently enables a lost update. Create
// writes hosts.yaml first, because a host with no sidecar entry is already
// a defined state (buildRecord reads it as version 0, StateActive) while a
// sidecar entry with no host is an orphan that findHostIndex can never
// match and no later write can clean up.
func (r *fileRepository) Create(ctx context.Context, item inventory.InventoryItem) error {
	if _, err := itemDeviceType(item); err != nil {
		return err
	}

	// Hold the mutex across the whole read-check-write sequence so a
	// concurrent Create or Save cannot interleave its own read and write of
	// these two files with this one.
	r.mu.Lock()
	defer r.mu.Unlock()

	original, err := os.ReadFile(r.hostsPath) // #nosec G304 -- hostsPath is derived from the repository's own configured project directory, not untrusted input
	if err != nil {
		// A project whose inventory file does not exist yet is a legitimate
		// starting point for a first sync, not an error: treat it as an
		// empty document and let the write below create it.
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read inventory file %s: %w", r.hostsPath, err)
		}
		original = nil
	}

	hosts, err := ParseHosts(original)
	if err != nil {
		return err
	}

	if err := ensureHostAbsent(hosts, item); err != nil {
		return err
	}

	spec, err := hostSpecFromItem(item)
	if err != nil {
		return err
	}
	hosts = append(hosts, spec)

	if err := writeHostsAtomic(r.hostsPath, original, hosts); err != nil {
		return fmt.Errorf("failed to write inventory for %s: %w", item.Name(), err)
	}

	// A device created at version 0 with no history and no provenance needs
	// no sidecar entry at all: buildRecord already reads a missing entry as
	// exactly that. Writing one anyway would add a row carrying nothing the
	// default does not already say.
	if item.Version() == 0 && len(item.History()) == 0 && item.Source().Plugin == "" &&
		item.State() == inventory.StateActive {
		return nil
	}

	return r.createSidecarEntry(item)
}

// ensureHostAbsent reports ErrItemExists if hosts already contains item's ID
// or name. Both are checked because both are unique in the ent adapter, and
// a Repository whose two backends disagree about what counts as a duplicate
// is not substitutable.
func ensureHostAbsent(hosts []HostSpec, item inventory.InventoryItem) error {
	for _, h := range hosts {
		if h.ID == string(item.ID()) || h.Name == item.Name() {
			return fmt.Errorf("creating %s: %w", item.Name(), ErrItemExists)
		}
	}
	return nil
}

// hostSpecFromItem builds the hosts.yaml row for a newly created item. It
// writes Type rather than Classify: classification already ran (that is
// where the item's type came from), and persisting the resolved answer is
// what ResolveHostType's own doc comment describes add-host doing, so a
// later load never has to re-derive it.
func hostSpecFromItem(item inventory.InventoryItem) (HostSpec, error) {
	deviceType, err := itemDeviceType(item)
	if err != nil {
		return HostSpec{}, err
	}

	tags := item.Tags()
	tagStrings := make([]string, 0, len(tags))
	for _, t := range tags {
		tagStrings = append(tagStrings, string(t))
	}

	return HostSpec{
		ID:         string(item.ID()),
		Name:       item.Name(),
		Type:       deviceType,
		Tags:       tagStrings,
		Properties: item.Properties().Raw(),
	}, nil
}

// createSidecarEntry writes the state half of a newly created device: its
// version, lifecycle state, audit trail, and provenance. It is split out of
// Create so the happy path above reads as the two file writes it actually
// is.
func (r *fileRepository) createSidecarEntry(item inventory.InventoryItem) error {
	sidecar, err := readSidecar(r.sidecarPath)
	if err != nil {
		return err
	}

	entry := sidecarEntry{
		ID:      string(item.ID()),
		Version: item.Version(),
		State:   item.State().String(),
		History: fromRevisions(item.History()),
	}
	if src := item.Source(); src.Plugin != "" {
		entry.Source = src.Plugin
		if !src.SyncedAt.IsZero() {
			syncedAt := src.SyncedAt
			entry.SourceSyncedAt = &syncedAt
		}
	}

	sidecar.Hosts = append(sidecar.Hosts, entry)
	if err := writeSidecar(r.sidecarPath, sidecar); err != nil {
		return fmt.Errorf("failed to write inventory state for %s: %w", item.Name(), err)
	}
	return nil
}

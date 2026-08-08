package inventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Save persists an item's mutated properties, lifecycle state, and every
// Revision recorded since it was loaded, conditional on the sidecar's
// stored version for this item's ID still matching the version the item
// was hydrated at. See the Repository interface's doc comment
// (iterator.go) for the full optimistic-concurrency contract; this
// implementation matches entRepository.Save's semantics exactly (see
// ent_save.go): no-op when unchanged, error when current version is below
// baseVersion, ErrVersionConflict when the stored row already moved.
//
// Properties (HostSpec's existing shape) land in hosts.yaml; version,
// lifecycle state, and history land in the sidecar, since HostSpec
// deliberately has no fields for them (see sidecarEntry's doc comment,
// file_repository_state.go). Both files are written by writing to a temp
// file in their own directory and os.Rename-ing over the target
// (atomicWriteFile), so a crash mid-write never leaves either file
// half-written. That is this file-backend's equivalent of the transaction
// guarantee entRepository.Save gets for free from a SQL transaction: the
// same guarantee (a reader never observes a torn write), delivered by a
// different mechanism appropriate to a medium with no transaction log.
// This is an adapter behind the same port, not a second codebase.
//
// The two files are not written as a single cross-file transaction,
// though: a crash between the two renames can leave them briefly
// inconsistent with each other. Save deliberately writes the sidecar
// (version, state, history) before hosts.yaml (properties), because that
// ordering is the safe one to crash into: if the process dies after the
// sidecar commits but before hosts.yaml does, the worst case is a stored
// property value that has not yet caught up with a version bump the audit
// trail already recorded, an observable staleness. The reverse order would
// risk the opposite: a properties change landing while the stored version
// stays put, which lets a later writer's version check pass falsely and
// silently overwrite the very change that just landed. That is exactly the
// lost update this whole method exists to prevent, so it must never be the
// failure mode a crash produces.
func (r *fileRepository) Save(ctx context.Context, item inventory.InventoryItem) error {
	// An item that cannot report the version it was loaded at cannot be
	// written safely, because there is no way to detect a lost update.
	v, ok := item.(versioned)
	if !ok {
		return fmt.Errorf("item %s does not report a base version, refusing to write without conflict detection", item.Name())
	}

	baseVersion := v.BaseVersion()
	current := item.Version()

	// Nothing changed since load. Writing anyway would burn a version and
	// produce a revision-free bump that later readers cannot explain.
	if current == baseVersion {
		return nil
	}
	if current < baseVersion {
		return fmt.Errorf("item %s has version %d below its loaded version %d, which should be impossible", item.Name(), current, baseVersion)
	}

	// Only the revisions recorded since load are new. Anything at or below
	// baseVersion is already in the sidecar and must not be duplicated.
	var pending []inventory.Revision
	for _, rev := range item.History() {
		if rev.Version > baseVersion {
			pending = append(pending, rev)
		}
	}

	// Hold the mutex across the whole read-check-write sequence: another
	// goroutine's Save must not interleave its own read and write of these
	// two files with this one.
	r.mu.Lock()
	defer r.mu.Unlock()

	original, err := os.ReadFile(r.hostsPath) // #nosec G304 -- hostsPath is derived from the repository's own configured project directory, not untrusted input
	if err != nil {
		return fmt.Errorf("failed to read inventory file %s: %w", r.hostsPath, err)
	}
	hosts, err := ParseHosts(original)
	if err != nil {
		return err
	}
	hostIdx := findHostIndex(hosts, item.ID())
	if hostIdx == -1 {
		return fmt.Errorf("cannot save item %s: no host with id %s found in %s", item.Name(), item.ID(), r.hostsPath)
	}

	sidecar, err := readSidecar(r.sidecarPath)
	if err != nil {
		return err
	}

	// The conflict check this whole method exists for: re-read the
	// sidecar's currently-stored version right before writing. A host
	// with no sidecar entry yet is implicitly at version 0 (entry is the
	// zero value, whose Version field is 0), matching buildRecord's
	// default for an unsaved host.
	entry, entryIdx := findSidecarEntry(sidecar, item.ID())
	if entry.Version != baseVersion {
		// Either the row moved to a newer version, or (impossibly, since
		// current >= baseVersion was already checked above) an older one.
		// Either way this change was computed against state that no longer
		// exists, so the caller must reload rather than retry.
		return fmt.Errorf("saving %s at version %d: %w", item.Name(), baseVersion, ErrVersionConflict)
	}

	entry.ID = string(item.ID())
	entry.Version = current
	entry.State = item.State().String()
	entry.History = append(entry.History, fromRevisions(pending)...)
	// Chain audit finding (IMPLEMENTATION.md Phase W4): mirrors
	// entRepository.Save's identical fix. Nothing in the base
	// InventoryItem contract can change Source today, so this writes back
	// exactly what was already loaded; the point is that the sidecar
	// stops being write-blind for provenance, not that this call changes
	// its value.
	if src := item.Source(); src.Plugin != "" {
		entry.Source = src.Plugin
		if !src.SyncedAt.IsZero() {
			syncedAt := src.SyncedAt
			entry.SourceSyncedAt = &syncedAt
		}
	}
	if entryIdx == -1 {
		sidecar.Hosts = append(sidecar.Hosts, entry)
	} else {
		sidecar.Hosts[entryIdx] = entry
	}

	// Write the sidecar first; see the method doc comment above for why
	// this ordering, not the reverse, is the one that fails safely.
	if err := writeSidecar(r.sidecarPath, sidecar); err != nil {
		return fmt.Errorf("failed to write inventory state for %s: %w", item.Name(), err)
	}

	hosts[hostIdx].Properties = item.Properties().Raw()
	if err := writeHostsAtomic(r.hostsPath, original, hosts); err != nil {
		return fmt.Errorf("failed to write inventory for %s: %w", item.Name(), err)
	}

	return nil
}

// findHostIndex returns the index of the host with id within hosts, or -1
// if none matches. Matching is always by ID, never Name: Name is mutable
// by design (DeviceID's doc comment, pkg/inventory/item.go), so matching
// on it would silently attach a Save to the wrong row after a rename.
func findHostIndex(hosts []HostSpec, id inventory.DeviceID) int {
	for i := range hosts {
		hostID := hosts[i].ID
		if hostID == "" {
			hostID = hosts[i].Name
		}
		if inventory.DeviceID(hostID) == id {
			return i
		}
	}
	return -1
}

// writeHostsAtomic serializes hosts through EncodeHosts, the same encoder
// WriteHosts (yaml_plugin.go) uses, merged into original (Save's own
// earlier read of the file, so the merge sees exactly the content it is
// replacing, not a second, possibly stale, read of it), and writes them
// through atomicWriteFile rather than through WriteHosts itself. WriteHosts
// writes with a plain os.WriteFile, which is fine for its callers (a human
// editor saving the file, or a first-time write), but Save specifically
// needs the crash-safe temp-file-plus-rename guarantee documented on
// atomicWriteFile.
func writeHostsAtomic(path string, original []byte, hosts []HostSpec) error {
	data, err := encodeHostsSafely(original, hosts)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o644)
}

// encodeHostsSafely calls EncodeHosts (yaml_plugin.go), converting a
// panic from the underlying YAML encoder into a normal error. See
// marshalSidecar's doc comment (file_repository_state.go) for why this
// guard exists: a HostSpec's Properties is a caller-controlled
// map[string]interface{}, and go.yaml.in/yaml/v3 cannot be trusted to
// always turn an encoding failure into a returned error, rather than a
// panic, for every possible Go value a caller might have stored there.
// In practice Save's sidecar-first write ordering means a bad revision
// value already fails writeSidecar before this function is ever reached
// for the same change, but this guard stays in place rather than relying
// on that ordering never changing.
func encodeHostsSafely(original []byte, hosts []HostSpec) (data []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unsupported property value: %v", r)
		}
	}()
	return EncodeHosts(original, hosts)
}

// atomicWriteFile writes data to path by first writing a temp file in the
// same directory, then os.Rename-ing it over path. Rename is atomic on
// POSIX filesystems within one directory, so a reader (or a crash) never
// observes a partially written file: it sees either the complete old
// content or the complete new content, never a torn write.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()

	// Clean up the temp file on any path that does not end in a
	// successful rename, so a failed Save never leaves stray files behind
	// in the inventory directory.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file for %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set permissions on temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file for %s: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to atomically replace %s: %w", path, err)
	}
	renamed = true
	return nil
}

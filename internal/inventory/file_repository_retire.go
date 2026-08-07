package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// Retire transitions the host named name to inventory.StateArchived and
// appends the transition to its sidecar history. See the Repository
// interface's doc comment (iterator.go) for the full contract; this
// implementation matches entRepository.Retire's semantics exactly
// (ent_retire.go): ErrItemNotFound for an unknown name, idempotent and
// revision-free for an already-archived host, and one recorded Revision
// otherwise.
//
// Only the sidecar is written. Lifecycle state, version, and history are
// precisely the three things HostSpec deliberately has no field for (see
// sidecarEntry's doc comment, file_repository_state.go), so hosts.yaml is
// read to resolve the name into an ID and is never rewritten. That also
// means this operation has no cross-file consistency question of the kind
// Save's own doc comment has to reason about: there is exactly one write.
func (r *fileRepository) Retire(_ context.Context, name string) error {
	// Hold the mutex across the whole read-check-write sequence, matching
	// Save: another goroutine must not interleave its own read and write
	// of the sidecar with this one.
	r.mu.Lock()
	defer r.mu.Unlock()

	hosts, err := ReadHosts(r.hostsPath)
	if err != nil {
		return err
	}

	// Resolve by Name, since that is what a caller addresses a host by,
	// then correlate to the sidecar by ID, since Name is mutable by design
	// and correlating history by it would attach one host's audit trail to
	// another after a rename. This is the same two-step GetByName performs.
	var hostID inventory.DeviceID
	found := false
	for i := range hosts {
		if hosts[i].Name != name {
			continue
		}
		id := hosts[i].ID
		if id == "" {
			id = hosts[i].Name
		}
		hostID = inventory.DeviceID(id)
		found = true
		break
	}
	if !found {
		return fmt.Errorf("host %s: %w", name, ErrItemNotFound)
	}

	sidecar, err := readSidecar(r.sidecarPath)
	if err != nil {
		return err
	}

	entry, entryIdx := findSidecarEntry(sidecar, hostID)

	// A host that has never been through Save has no sidecar entry, so it
	// is implicitly at version 0 in whatever state buildRecord defaults to.
	// Retiring it is legitimate and creates the entry, which is the same
	// treatment Save gives a first write.
	current := inventory.StateDiscovered
	if entry.State != "" {
		current, err = inventory.ParseLifecycleState(entry.State)
		if err != nil {
			return fmt.Errorf("host %s holds an unrecognized lifecycle state: %w", name, err)
		}
	}

	// Idempotent, matching the HTTP verb this backs and ent_retire.go. A
	// second retirement records no second Revision: an audit trail saying
	// a host was archived twice describes something that never happened.
	if current == inventory.StateArchived {
		return nil
	}

	nextVersion := entry.Version + 1

	entry.ID = string(hostID)
	entry.Version = nextVersion
	entry.State = inventory.StateArchived.String()
	entry.History = append(entry.History, sidecarRevision{
		Version:   nextVersion,
		ChangedAt: time.Now().UTC(),
		Field:     retiredRevisionField,
		OldValue:  current.String(),
		NewValue:  inventory.StateArchived.String(),
	})

	if entryIdx == -1 {
		sidecar.Hosts = append(sidecar.Hosts, entry)
	} else {
		sidecar.Hosts[entryIdx] = entry
	}

	if err := writeSidecar(r.sidecarPath, sidecar); err != nil {
		return fmt.Errorf("failed to write inventory state for %s: %w", name, err)
	}
	return nil
}

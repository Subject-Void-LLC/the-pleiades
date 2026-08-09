package inventory

import (
	"fmt"
	"os"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"go.yaml.in/yaml/v3"
)

// sidecarFileName is the fixed filename NewFileRepository derives the
// sidecar path from, always placed in the same directory as the primary
// inventory file. The leading dot and "generated" wording mark it as
// machinery a human should not hand-edit, the same convention a lockfile
// uses (package-lock.json, Cargo.lock): hosts.yaml stays the simple,
// hand-editable file Walk tier promises (yaml_plugin.go); this file is
// where the version and audit trail HostSpec deliberately does not carry
// actually live.
const sidecarFileName = ".inventory-state.generated.yaml"

// sidecarDocument is the on-disk shape of the sidecar file: one entry per
// host, keyed by ID.
type sidecarDocument struct {
	Hosts []sidecarEntry `yaml:"hosts"`
}

// sidecarEntry carries exactly what hosts.yaml deliberately does not: the
// optimistic-concurrency version, the lifecycle state (HostSpec has no
// field for it), and the audit trail. It is the file-backend analogue of
// the ent schema's version/state columns plus its separate Revision table
// (internal/ent/schema/device.go, internal/ent/schema/revision.go),
// applying HANDOFF_DOCUMENT.md's "why a table, not a blob column" split
// ("the questions it answers are cross-device and cross-time and a blob
// cannot be indexed for those") to a file pair instead of a database.
//
// ID correlates an entry to its HostSpec row. It is never Name: Name is
// mutable by design (DeviceID's doc comment, pkg/inventory/item.go), and
// correlating by it would silently attach one host's history to another
// after a rename.
type sidecarEntry struct {
	ID      string            `yaml:"id"`
	Version uint64            `yaml:"version"`
	State   string            `yaml:"state"`
	History []sidecarRevision `yaml:"history,omitempty"`
	// Source and SourceSyncedAt are the file-backend analogue of the ent
	// schema's source/source_synced_at columns (chain audit finding,
	// IMPLEMENTATION.md Phase W4): before this, sidecarEntry had no field
	// for provenance at all, so buildRecord hardcoded every file-backed
	// device to Plugin: "file" with no way to record which source last
	// synced it. Empty/zero means "never recorded," in which case
	// buildRecord still falls back to the "file" default.
	Source         string     `yaml:"source,omitempty"`
	SourceSyncedAt *time.Time `yaml:"source_synced_at,omitempty"`
}

// sidecarRevision is the on-disk form of inventory.Revision.
type sidecarRevision struct {
	Version   uint64                  `yaml:"version"`
	ChangedAt time.Time               `yaml:"changed_at"`
	Field     string                  `yaml:"field"`
	OldValue  inventory.PropertyValue `yaml:"old_value,omitempty"`
	NewValue  inventory.PropertyValue `yaml:"new_value,omitempty"`
}

// readSidecar loads the sidecar file, returning an empty document rather
// than an error if it does not exist yet. A fresh hosts.yaml with no prior
// Save calls has no sidecar file, and every host in it is implicitly at
// version 0 with no history: the zero value of sidecarDocument represents
// exactly that state, so callers do not need a separate not-yet-created
// branch.
func readSidecar(path string) (sidecarDocument, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from the repository's own configured project directory, not untrusted input
	if err != nil {
		if os.IsNotExist(err) {
			return sidecarDocument{}, nil
		}
		return sidecarDocument{}, fmt.Errorf("failed to read inventory state file %s: %w", path, err)
	}

	var doc sidecarDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return sidecarDocument{}, fmt.Errorf("failed to unmarshal inventory state file %s: %w", path, err)
	}
	return doc, nil
}

// writeSidecar serializes and atomically writes the sidecar document. See
// atomicWriteFile (file_repository.go) for the crash-safety guarantee this
// provides.
func writeSidecar(path string, doc sidecarDocument) error {
	data, err := marshalSidecar(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal inventory state file %s: %w", path, err)
	}
	return atomicWriteFile(path, data, 0o644)
}

// marshalSidecar serializes doc to YAML, converting a panic from the
// underlying encoder into a normal error rather than letting it escape.
// OldValue/NewValue on a stored revision (sidecarRevision) are
// PropertyValue, an alias for any (pkg/inventory/item.go): AddInfo lets a
// caller record literally any Go value, including ones
// go.yaml.in/yaml/v3 cannot encode at all (a channel, a func). Its
// Marshal wraps most encoding failures in a recoverable error already,
// but a handful of reflect kinds it has no case for reach an internal
// panic that escapes its own recover. Since a property value's type is
// entirely caller-controlled, Save must survive that rather than crash
// the process; this is the one seam in this file where an
// interface{}-typed value from outside this codebase reaches a marshaler
// that cannot be trusted to always turn its own limitation into a
// returned error.
func marshalSidecar(doc sidecarDocument) (data []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unsupported property value in revision history: %v", r)
		}
	}()
	return yaml.Marshal(doc)
}

// findSidecarEntry returns the entry for id and its index within
// doc.Hosts, or the zero value and -1 if the host has no sidecar entry
// yet (meaning it has never been through Save).
func findSidecarEntry(doc sidecarDocument, id inventory.DeviceID) (sidecarEntry, int) {
	for i, e := range doc.Hosts {
		if inventory.DeviceID(e.ID) == id {
			return e, i
		}
	}
	return sidecarEntry{}, -1
}

// toRevisions converts a sidecar entry's stored history into the domain
// Revision type record.Record carries. A nil/empty input yields a nil
// slice rather than an empty one, matching History()'s "empty means not
// loaded, not never changed" convention (record.Record's doc comment).
func toRevisions(rows []sidecarRevision) []inventory.Revision {
	if len(rows) == 0 {
		return nil
	}
	revs := make([]inventory.Revision, len(rows))
	for i, row := range rows {
		revs[i] = inventory.Revision{
			Version:   row.Version,
			ChangedAt: row.ChangedAt,
			Field:     row.Field,
			OldValue:  row.OldValue,
			NewValue:  row.NewValue,
		}
	}
	return revs
}

// fromRevisions converts pending domain Revisions into their on-disk
// sidecar form, for appending onto a sidecar entry's stored history.
func fromRevisions(revs []inventory.Revision) []sidecarRevision {
	if len(revs) == 0 {
		return nil
	}
	rows := make([]sidecarRevision, len(revs))
	for i, rev := range revs {
		rows[i] = sidecarRevision{
			Version:   rev.Version,
			ChangedAt: rev.ChangedAt,
			Field:     rev.Field,
			OldValue:  rev.OldValue,
			NewValue:  rev.NewValue,
		}
	}
	return rows
}

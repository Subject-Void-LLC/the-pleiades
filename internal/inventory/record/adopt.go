// Recording a sync's adoption of a device added by hand, as a revision, so
// the change of owner is saved and its history says when it happened.
package record

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// RevisionFieldSource is the history field an adoption records: the sync
// plugin that owned the device before, and the one that owns it now.
const RevisionFieldSource = "source"

// RecordAdoption records that the device's owner, its Source, changed from
// previous to the one it was built with, as a revision. A repository saves
// an item only when its version moved, so without it a sync adopting a
// device whose properties already match upstream would save nothing, and
// the next sync would find it hand-added again. It does nothing when the
// owner did not change.
func (b *Base) RecordAdoption(previous string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if previous == b.source.Plugin {
		return false
	}
	b.version++
	b.history = append(b.history, inventory.Revision{
		Version:   b.version,
		ChangedAt: time.Now().UTC(),
		Field:     RevisionFieldSource,
		OldValue:  previous,
		NewValue:  b.source.Plugin,
	})
	return true
}

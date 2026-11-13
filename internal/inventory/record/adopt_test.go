// RecordAdoption: a change of owner is one revision, and no change none.
package record

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func TestRecordAdoption(t *testing.T) {
	b := NewBase(Record{Name: "web1", Source: inventory.SourceAuthority{Plugin: "vbox"}, Version: 3}, nil)
	if b.RecordAdoption("vbox") || b.Version() != 3 {
		t.Fatalf("the same owner recorded a revision: version %d", b.Version())
	}
	if !b.RecordAdoption("") || b.Version() != 4 {
		t.Fatalf("an adoption recorded no revision: version %d", b.Version())
	}
	h := b.History()
	if last := h[len(h)-1]; last.Field != RevisionFieldSource || last.OldValue != "" || last.NewValue != "vbox" || last.Version != 4 {
		t.Errorf("revision %+v", last)
	}
}

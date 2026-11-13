// How the model answers list --long hdds: each machine's disks attached to
// it, a linked clone's descending from its source's, read back through
// pkg/vboxmanage's own parser.
package vboxmanagetest

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func TestListMedia_ModelsLinkedClones(t *testing.T) {
	const snap = "3c7f4f7e-2a47-4a4d-9b0e-6a4d2f1b8c11"
	base := &VM{Name: "base", UUID: "7d0b3f7c-4a30-4c43-9b3b-2d8f31c4d7a1", State: vboxmanage.StatePoweroff,
		Snapshots: []Snapshot{{Name: "base", UUID: snap}}, Current: snap,
		Slots: []vboxmanage.Slot{{Controller: "SATA", Medium: `G:\Lab\base\base.vdi`}, {Controller: "IDE", Port: 1, Medium: `G:\Lab\base\seed.iso`}}}
	other := &VM{Name: "other", UUID: "11111111-1111-4111-8111-111111111111", State: vboxmanage.StatePoweroff,
		Slots: []vboxmanage.Slot{{Controller: "SATA", Medium: `G:\Lab\other\other.vmdk`}}}
	model := New(base, other)
	h := model.VBoxHost()
	ctx := context.Background()
	if err := h.CloneLinked(ctx, "base", snap, "clone", `G:\Lab`); err != nil {
		t.Fatal(err)
	}
	clone := model.VM("clone")
	// A clone of the clone: its disk descends from the clone's, and so
	// from the base's.
	clone.Snapshots, clone.Current = []Snapshot{{Name: "s", UUID: snap}}, snap
	if err := h.CloneLinked(ctx, "clone", snap, "grandchild", `G:\Lab`); err != nil {
		t.Fatal(err)
	}
	media, err := h.Media(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 4 {
		t.Fatalf("%d disks, want 4 (base, other, clone, grandchild; never the seed ISO): %+v", len(media), media)
	}
	names := func(us []vboxmanage.MediumUser) map[string]bool {
		m := map[string]bool{}
		for _, u := range us {
			m[u.Name] = true
		}
		return m
	}
	if got := names(vboxmanage.LinkedClones(media, base.UUID)); len(got) != 2 || !got["clone"] || !got["grandchild"] {
		t.Errorf("the base's clones: %v", got)
	}
	if got := vboxmanage.LinkedClones(media, other.UUID); len(got) != 0 {
		t.Errorf("an unrelated VM has clones: %+v", got)
	}
	// A source VirtualBox cannot read is still listed as the parent its
	// clones know, used by nothing.
	base.Inaccessible = true
	media, _ = h.Media(ctx)
	for _, m := range media {
		if m.Location == `G:\Lab\base\base.vdi` && len(m.Users) != 0 {
			t.Errorf("an unreadable base's disk lists users %+v", m.Users)
		}
	}
	if got := names(vboxmanage.LinkedClones(media, base.UUID)); !got["clone"] {
		t.Errorf("the unreadable base's clones: %v", got)
	}
}

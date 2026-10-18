// Forget and Ref.Inaccessible, against the model host.
package vboxmanage_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestForget_UnregistersByUUIDOnly: an inaccessible VM lists under its
// placeholder name and is forgotten by its UUID; a name that is not a UUID
// is refused before VirtualBox is asked.
func TestForget_UnregistersByUUIDOnly(t *testing.T) {
	const ghost = "9883f6c3-b17d-4077-aa52-d9c49a6912a5"
	model := vboxmanagetest.New(&vboxmanagetest.VM{Name: "gone", UUID: ghost, State: vboxmanage.StatePoweroff, Inaccessible: true})
	h := model.VBoxHost()
	ctx := context.Background()
	refs, err := h.List(ctx)
	if err != nil || len(refs) != 1 || !refs[0].Inaccessible() || refs[0].Name != vboxmanage.InaccessibleName {
		t.Fatalf("list = %+v, %v", refs, err)
	}
	if err := h.Forget(ctx, "gone"); err == nil {
		t.Error("a name that is not a UUID was forgotten")
	}
	if err := h.Forget(ctx, ghost); err != nil {
		t.Fatal(err)
	}
	if refs, _ := h.List(ctx); len(refs) != 0 {
		t.Errorf("after forgetting: %+v", refs)
	}
	if (vboxmanage.Ref{Name: "lab"}).Inaccessible() {
		t.Error("a named VM reads as inaccessible")
	}
}

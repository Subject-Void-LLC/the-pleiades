// Tests that a skew digests its Collection's group only when it must
// (skew.go): GroupDigest encodes every manifest of a group, and the
// Controller asks Verify of every step it plans.
package rollback

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSkew_DigestsTheGroupOnlyWhenItMustCompareOrSayIt: an undo whose
// digest was recorded and is this build's is decided without the group's
// digest; one that recorded no undo digest needs it to decide, and a
// message naming the group's digest needs it to say so.
func TestSkew_DigestsTheGroupOnlyWhenItMustCompareOrSayIt(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	target := collection.Manifest{Status: collection.StatusImplemented, Doc: collection.Doc{Params: []collection.Param{{Name: "path", Type: "string"}}}}
	spec := sdk.InverseSpec{FQCN: "file.rblazyremove", Record: []string{"path"}}
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "file.rblazymake", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
		Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec}}}}); err != nil {
		t.Fatal(err)
	}
	id := wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: wire.ManifestDigestPrefix + strings.Repeat("a", 64)}
	undo := skewOf("file.rblazymake", id, "", spec, target, groupDigests{}).undoDigest

	groups := groupDigests{}
	k := skewOf("file.rblazymake", id, undo, spec, target, groups)
	if k.skewed() || len(groups) != 0 {
		t.Errorf("an undo recorded under this build's declarations: skewed %v, digested %v; want neither", k.skewed(), groups)
	}
	_ = k.describe()
	if len(groups) != 1 {
		t.Errorf("a message naming the group's digest digested %v, want the file group", groups)
	}

	groups = groupDigests{}
	if k := skewOf("file.rblazymake", id, "", spec, target, groups); !k.skewed() || len(groups) != 1 {
		t.Errorf("an entry with no undo digest: skewed %v, digested %v; want it decided by the group's digest", k.skewed(), groups)
	}
}

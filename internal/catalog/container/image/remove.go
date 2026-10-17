// container.image.remove: an image, or one of its tags, removed through
// one body its check shares.
package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnRemove = "container.image.remove"

func init() {
	register(fqcnRemove, containerengine.MethodManifest(collection.Reversibility{
		Reversible: false,
		Notes:      "A removed image's content is gone from the engine; container.image.pull brings an image from a registry back, and container.image.load one from a saved archive.",
	}, removeDoc()), remove)
}

// remove is container.image.remove's one body. An absent image is no
// change; one a container uses, in any state, is refused unless force is
// set; with id, a reference that now names another image is refused.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnRemove)
	ref, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return fail(err)
	}
	if err := validImageRef(paramImage, ref); err != nil {
		return fail(err)
	}
	want := sdk.StringParam(params, paramID)
	if want != "" && !isImageID(want) {
		return fail(fmt.Errorf("%s: %q is not an image ID", paramID, want))
	}
	force, err := sdk.BoolParamOr(params, paramForce, false)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnRemove,
		containerengine.OpImageInspect, containerengine.OpContainerList, containerengine.OpImageRemove)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	img, exists, err := inspect(ctx, s, ref)
	if err != nil {
		return fail(err)
	}
	if err := rc.SetStat(statImage, ref); err != nil {
		return collection.Result{}, err
	}
	if !exists {
		return collection.Result{}, nil
	}
	if want != "" && strings.TrimPrefix(img.ID, "sha256:") != strings.TrimPrefix(want, "sha256:") {
		return fail(fmt.Errorf("%s now names image %s, not %s, so this task, recorded against the other, does nothing to it", ref, img.ID, want))
	}
	users, err := containersUsing(ctx, s)
	if err != nil {
		return fail(err)
	}
	if err := recordStats(rc, map[string]any{statImageID: img.ID, statUsedBy: users[img.ID]}); err != nil {
		return collection.Result{}, err
	}
	if len(users[img.ID]) > 0 && !force {
		return fail(fmt.Errorf("image %s is used by container %s; remove them first, or set %s", ref, strings.Join(users[img.ID], ", "), paramForce))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	// An image named by ID goes with every name it has, which the engine
	// does only when forced; the check above already refused one in use.
	res, err := s.API.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: force || isImageID(ref), PruneChildren: true})
	if err != nil {
		return fail(fmt.Errorf("removing %s: %w", ref, err))
	}
	var removed []string
	for _, item := range res.Items {
		if item.Untagged != "" {
			removed = append(removed, item.Untagged)
		}
		if item.Deleted != "" {
			removed = append(removed, item.Deleted)
		}
	}
	return collection.Result{Changed: true}, rc.SetStat(statRemoved, removed)
}

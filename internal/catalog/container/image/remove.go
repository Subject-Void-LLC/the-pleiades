// container.image.remove: an image, or one of its tags, removed through
// one body its check shares.
package image

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
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
	want, err := containerengine.TextParam(params, paramID)
	if err != nil {
		return fail(err)
	}
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
	// A tag of an image that keeps another name is only untagged, which no
	// container notices: they run by the image's ID.
	untagOnly := !isImageID(ref) && len(img.RepoTags) > 1
	if len(users[img.ID]) > 0 && !force && !untagOnly {
		return fail(fmt.Errorf("image %s is used by container %s; remove them first, or set %s", ref, strings.Join(users[img.ID], ", "), paramForce))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	// The engine removes an image named by ID with several names only when
	// forced, and Podman's force also removes the containers using it, so
	// without a task's force each name goes first and then the ID.
	refs := []string{ref}
	if isImageID(ref) && !force {
		refs = append(slices.Clone(img.RepoTags), img.ID)
	}
	var removed []string
	for _, r := range refs {
		res, err := s.API.ImageRemove(ctx, r, client.ImageRemoveOptions{Force: force, PruneChildren: true})
		if cerrdefs.IsNotFound(err) && r == img.ID && len(refs) > 1 {
			// Its last name went, and took the image with it.
			break
		}
		if err != nil {
			return collection.Result{Changed: len(removed) > 0}, errors.Join(rc.SetStat(statRemoved, removed), fmt.Errorf("%s: removing %s: %w", fqcnRemove, r, err))
		}
		for _, item := range res.Items {
			if item.Untagged != "" {
				removed = append(removed, item.Untagged)
			}
			if item.Deleted != "" {
				removed = append(removed, item.Deleted)
			}
		}
	}
	return collection.Result{Changed: true}, rc.SetStat(statRemoved, removed)
}

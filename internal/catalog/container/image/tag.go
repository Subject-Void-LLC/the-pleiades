// container.image.tag: an image named by another tag, through one body
// its check shares.
package image

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnTag = "container.image.tag"

func init() {
	register(fqcnTag, containerengine.MethodManifest(collection.Reversibility{
		Reversible: true,
		Notes:      "A run that moved the tag records pointing it back at the image it named before, guarded by the image it names now; one that added a new tag records removing it, by the image's ID, unless the tag is the image's only name, since removing that deletes the image; one that found the tag already there records nothing.",
		Inverses: []sdk.InverseSpec{
			{FQCN: fqcnTag, Record: []string{paramSource, paramTarget, paramID, containerengine.ParamEngine}},
			{FQCN: fqcnRemove, Record: []string{paramImage, paramID, containerengine.ParamEngine}},
		},
	}, tagDoc()), tag)
}

// tag is container.image.tag's one body: the tag names the image source
// names.
func tag(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnTag)
	source, err := sdk.RequiredStringParam(params, paramSource)
	if err != nil {
		return fail(err)
	}
	if err := validImageRef(paramSource, source); err != nil {
		return fail(err)
	}
	raw, err := sdk.RequiredStringParam(params, paramTarget)
	if err != nil {
		return fail(err)
	}
	target, err := taggedRef(paramTarget, raw)
	if err != nil {
		return fail(err)
	}
	want, err := containerengine.TextParam(params, paramID)
	if err != nil {
		return fail(err)
	}
	if want != "" && !isImageID(want) {
		return fail(fmt.Errorf("%s: %q is not an image ID", paramID, want))
	}
	s, err := open(ctx, rc, device, params, fqcnTag, containerengine.OpImageInspect, containerengine.OpImageTag)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	src, exists, err := inspect(ctx, s, source)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return fail(fmt.Errorf("there is no image %s on the engine", source))
	}
	prior, had, err := inspect(ctx, s, target)
	if err != nil {
		return fail(err)
	}
	if want != "" && (!had || !sameImage(prior.ID, want)) {
		now := "no image"
		if had {
			now = "image " + prior.ID
		}
		return fail(fmt.Errorf("%s now names %s, not %s, so this task, recorded against the other, does nothing to it", target, now, want))
	}
	if err := recordStats(rc, map[string]any{statImage: target, statImageID: src.ID}); err != nil {
		return collection.Result{}, err
	}
	if had && prior.ID == src.ID {
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	if _, err := s.API.ImageTag(ctx, client.ImageTagOptions{Source: src.ID, Target: target}); err != nil {
		return fail(fmt.Errorf("naming %s %s: %w", source, target, err))
	}
	if had {
		return collection.Result{Changed: true}, containerengine.RecordUndo(rc, s, sdk.Inverse{
			FQCN:        fqcnTag,
			Params:      map[string]any{paramSource: prior.ID, paramTarget: target, paramID: src.ID},
			Description: "Point the tag back at the image it named before this task.",
		})
	}
	if len(src.RepoTags) == 0 {
		// The tag is the image's only name, and removing an image's last
		// name deletes the image, which was there before this task.
		return collection.Result{Changed: true}, nil
	}
	return collection.Result{Changed: true}, containerengine.RecordUndo(rc, s, sdk.Inverse{
		FQCN:        fqcnRemove,
		Params:      map[string]any{paramImage: target, paramID: src.ID},
		Description: "Remove the tag this task added.",
	})
}

// container.image.pull: an image present on the engine, pulled by digest,
// and named locally by the tag the task asked for.
package image

import (
	"context"
	"fmt"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnPull = "container.image.pull"

// pullOps are the operations pull sends.
var pullOps = append([]containerengine.Op{containerengine.OpImageTag}, containerengine.EnsureImageOps...)

func init() {
	register(fqcnPull, containerengine.MethodManifest(collection.Reversibility{
		Reversible: true,
		Notes:      "A run that moved an existing tag records pointing it back, guarded by the image it names now, and partial when the run also pulled, since the pulled image stays (container.image.prune removes it); one that pulled an absent image under a new tag records its removal, by ID; one that pointed a new tag at an image already there records removing the tag, unless it is the image's only name; one that found both as asked records nothing.",
		Inverses: []sdk.InverseSpec{
			{FQCN: fqcnRemove, Record: []string{paramImage, paramID, containerengine.ParamEngine}},
			{FQCN: fqcnTag, Record: []string{paramSource, paramTarget, paramID, containerengine.ParamEngine}, MayBePartial: true},
		},
	}, pullDoc()), pull)
}

// pull is container.image.pull's one body: pin the reference to a digest,
// make that image present (pulled by digest), and point the asked tag at
// it.
func pull(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnPull)
	ref, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidImage(ref); err != nil {
		return fail(err)
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return fail(err)
	}
	// The tag the image is named by locally: the one asked for, or none for
	// a reference by digest alone.
	tag := ""
	if _, digested := named.(reference.Digested); !digested {
		tag = reference.FamiliarString(reference.TagNameOnly(named))
	}
	auth, err := containerengine.RegistryAuth(rc, ref)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnPull, pullOps...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()

	var before string
	if tag != "" {
		prior, exists, err := inspect(ctx, s, tag)
		if err != nil {
			return fail(err)
		}
		if exists {
			before = prior.ID
		}
	}
	img, err := s.EnsureImage(ctx, ref, auth, mode == collection.ModeExecute)
	if err != nil {
		return fail(err)
	}
	retag := tag != "" && (img.ID == "" || before != img.ID)
	if err := recordStats(rc, map[string]any{
		statImage: img.Pinned, statDigest: img.Digest, statImageID: img.ID, statPulled: img.Pulled, statTagged: retag,
	}); err != nil {
		return collection.Result{}, err
	}
	if !img.Pulled && !retag {
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	onlyName := false
	if retag && before == "" && !img.Pulled {
		present, _, err := inspect(ctx, s, img.ID)
		if err != nil {
			return fail(err)
		}
		onlyName = len(present.RepoTags) == 0
	}
	if retag {
		if _, err := s.API.ImageTag(ctx, client.ImageTagOptions{Source: img.ID, Target: tag}); err != nil {
			if img.Pulled {
				// A failed task's undo is dropped (FAILURE_PATTERNS 452), so
				// the image this run pulled is removed here instead.
				cctx, cancel := containerengine.CleanupContext(ctx)
				defer cancel()
				_, _ = s.API.ImageRemove(cctx, img.ID, client.ImageRemoveOptions{})
				return fail(fmt.Errorf("naming %s %s: %w; the image this task pulled was removed", img.Pinned, tag, err))
			}
			return fail(fmt.Errorf("naming %s %s: %w", img.Pinned, tag, err))
		}
	}
	return collection.Result{Changed: true}, recordPullUndo(rc, s, img, tag, before, retag, onlyName)
}

// recordPullUndo records what reverses a pull. A tag that named another
// image is pointed back, guarded by the image it names now, partial when
// the image was also pulled: one undo cannot also remove it, and the tag
// is what a user reads. An image pulled under a new tag is removed, by ID
// (nothing can be using an image that was not there). A new tag on an
// image already there is removed, unless it is the image's only name,
// since removing that deletes the image.
func recordPullUndo(rc sdk.RunbookContext, s *containerengine.Session, img containerengine.PresentImage, tag, before string, retag, onlyName bool) error {
	switch {
	case retag && before != "":
		inv := sdk.Inverse{
			FQCN:        fqcnTag,
			Params:      map[string]any{paramSource: before, paramTarget: tag, paramID: img.ID},
			Description: "Point the tag back at the image it named before this task.",
		}
		if img.Pulled {
			inv.Partial = true
			inv.Description = "Point the tag back at the image it named before this task; the image this task pulled stays, untagged, for container.image.prune."
		}
		return containerengine.RecordUndo(rc, s, inv)
	case img.Pulled:
		return containerengine.RecordUndo(rc, s, sdk.Inverse{
			FQCN:        fqcnRemove,
			Params:      map[string]any{paramImage: img.ID, paramID: img.ID},
			Description: "Remove the image this task pulled.",
		})
	case retag && !onlyName:
		return containerengine.RecordUndo(rc, s, sdk.Inverse{
			FQCN:        fqcnRemove,
			Params:      map[string]any{paramImage: tag, paramID: img.ID},
			Description: "Remove the tag this task added.",
		})
	}
	return nil
}

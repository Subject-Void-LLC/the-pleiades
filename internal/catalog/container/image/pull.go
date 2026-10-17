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
		Notes:      "A run that pulled an absent image records its removal, by ID; one that only pointed the tag at the image records pointing it back, or removing the tag when it was new; one that found both as asked records nothing.",
		Inverses: []sdk.InverseSpec{
			{FQCN: fqcnRemove, Record: []string{paramImage, paramID}},
			{FQCN: fqcnTag, Record: []string{paramSource, paramTarget}},
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
	if retag {
		if _, err := s.API.ImageTag(ctx, client.ImageTagOptions{Source: img.ID, Target: tag}); err != nil {
			return fail(fmt.Errorf("naming %s %s: %w", img.Pinned, tag, err))
		}
	}
	return collection.Result{Changed: true}, recordPullUndo(rc, img, tag, before, retag)
}

// recordPullUndo records what reverses a pull: removing the image when it
// was absent (nothing can be using an image that was not there), else
// pointing the tag back where it was, or removing a tag that was new.
func recordPullUndo(rc sdk.RunbookContext, img containerengine.PresentImage, tag, before string, retag bool) error {
	switch {
	case img.Pulled:
		return sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        fqcnRemove,
			Params:      map[string]any{paramImage: img.ID, paramID: img.ID},
			Description: "Remove the image this task pulled.",
		})
	case retag && before != "":
		return sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        fqcnTag,
			Params:      map[string]any{paramSource: before, paramTarget: tag},
			Description: "Point the tag back at the image it named before this task.",
		})
	case retag:
		return sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:        fqcnRemove,
			Params:      map[string]any{paramImage: tag, paramID: img.ID},
			Description: "Remove the tag this task added.",
		})
	}
	return nil
}

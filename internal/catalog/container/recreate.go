// container.recreate: a container replaced by its declaration when it differs.
package container

import (
	"context"
	"fmt"

	moby "github.com/moby/moby/api/types/container"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRecreate,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: false,
			Notes:      "Recreating a container removes the old one, whose writable layer and anonymous volumes are gone; its declaration can make a new one, never the old.",
		}, recreateDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return recreate(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return recreate(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// recreate is container.recreate's body: an absent container is created
// and started; one that matches the declaration is left alone (started if
// stopped) unless force is set; one that differs, or any with force, is
// removed and made again from the declaration.
func recreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRecreate, err)
	}
	force, err := sdk.BoolParamOr(params, paramForce, false)
	if err != nil {
		return fail(err)
	}
	spec, err := containerengine.ParseSpec(params)
	if err != nil {
		return fail(err)
	}
	if spec.Image == "" {
		return fail(fmt.Errorf("image is required"))
	}
	auth, err := containerengine.RegistryAuth(rc, spec.Image)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnRecreate, convergeOps...)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	live, exists, err := inspect(ctx, s, spec.Name)
	if err != nil {
		return fail(err)
	}
	if err := guardIDIf(params, live, exists); err != nil {
		return fail(err)
	}
	img, err := presentImage(ctx, s, spec, auth, mode)
	if err != nil {
		return fail(err)
	}
	spec.ImageID = img.ID
	if err := recordStats(rc, map[string]any{
		statName: spec.Name, statImage: img.Pinned, statDigest: img.Digest, statImageID: img.ID,
		statEnv: spec.EnvNames(), statPulled: img.Pulled,
	}); err != nil {
		return collection.Result{}, err
	}
	var diffs []string
	if exists {
		if img.ID == "" {
			diffs = []string{"image: the declared image is not on the engine yet"}
		} else {
			diffs = spec.Differences(live)
		}
		if err := recordStats(rc, map[string]any{statID: live.ID, "differences": diffs}); err != nil {
			return collection.Result{}, err
		}
	}
	if exists && len(diffs) == 0 && !force {
		return existing(ctx, rc, s, spec, live, img, mode, true, false)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statState, "running")
	}
	if exists {
		removeQuietly(s, live.ID)
		if _, still, err := inspect(ctx, s, spec.Name); err != nil || still {
			return fail(fmt.Errorf("container %s could not be removed to recreate it", spec.Name))
		}
	}
	// A recreate records no undo: the old container cannot come back.
	res, err := createNew(ctx, rc, s, spec, true, false)
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// guardIDIf applies guardID to a container that exists.
func guardIDIf(params map[string]any, live moby.InspectResponse, exists bool) error {
	if !exists {
		return nil
	}
	return guardID(params, live)
}

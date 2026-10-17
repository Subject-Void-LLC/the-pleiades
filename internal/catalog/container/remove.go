// container.remove: a container removed, a running one only with force.
package container

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "container.remove",
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: false,
			Notes:      "A removed container's writable layer, and any anonymous volume removed with it, is gone; container.run makes a new one from its declaration.",
		}, lifecycleDoc("container.remove")),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// remove is container.remove's body: an absent container is no change; a
// running one is refused unless force is set.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "container.remove"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := containerengine.ValidName("container", name); err != nil {
		return fail(err)
	}
	force, err := sdk.BoolParamOr(params, paramForce, false)
	if err != nil {
		return fail(err)
	}
	volumes, err := sdk.BoolParamOr(params, paramVolumes, false)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcn, containerengine.OpContainerInspect, containerengine.OpContainerRemove)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	c, exists, err := inspect(ctx, s, name)
	if err != nil {
		return fail(err)
	}
	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, err
	}
	if !exists {
		return collection.Result{}, nil
	}
	if err := guardID(params, c); err != nil {
		return fail(err)
	}
	if err := recordStats(rc, map[string]any{statID: c.ID, statState: stateOf(c)}); err != nil {
		return collection.Result{}, err
	}
	if state := stateOf(c); (state == "running" || state == "restarting" || state == "paused") && !force {
		return fail(fmt.Errorf("container %s is %s; stop it first, or set %s", name, state, paramForce))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statState, "removed")
	}
	if _, err := s.API.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: force, RemoveVolumes: volumes}); err != nil {
		return fail(fmt.Errorf("container %s: %w", name, err))
	}
	return collection.Result{Changed: true}, rc.SetStat(statState, "removed")
}

// container.volume.remove: remove a volume no container uses, through one
// body its check shares.
package volume

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

const fqcnRemove = "container.volume.remove"

// statRemoved says the volume was removed, or by a check would be.
const statRemoved = "removed"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRemove,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: false,
			Notes:      "A removed volume's data is gone: container.volume.create makes an empty one, and nothing brings the data back.",
		}, removeDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// remove is container.volume.remove's body: an absent volume is no change;
// one any container mounts, running or stopped, is refused.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRemove, err)
	}
	name, err := validName(params)
	if err != nil {
		return fail(err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnRemove,
		containerengine.OpVolumeInspect, containerengine.OpContainerList, containerengine.OpVolumeRemove)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, err
	}
	live, exists, err := inspectVolume(ctx, s, name)
	if err != nil {
		return fail(err)
	}
	if !exists {
		return collection.Result{}, nil
	}
	if err := guardCreated(params, live); err != nil {
		return fail(err)
	}
	if err := rc.SetStat(statCreatedAt, live.CreatedAt); err != nil {
		return collection.Result{}, err
	}
	users, err := usersOf(ctx, s, name)
	if err != nil {
		return fail(err)
	}
	if len(users) > 0 {
		return fail(fmt.Errorf("volume %s is mounted by %s; remove them first", name, strings.Join(users, ", ")))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statRemoved, true)
	}
	if _, err := s.API.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); err != nil {
		return fail(fmt.Errorf("volume %s: %w", name, err))
	}
	return collection.Result{Changed: true}, rc.SetStat(statRemoved, true)
}

// container.volume.info: read a volume as the engine reports it.
package volume

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnInfo = "container.volume.info"

// Returns only info makes.
const (
	statMountpoint  = "mountpoint"
	statScope       = "scope"
	statLabels      = "labels"
	statOptionNames = "option_names"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnInfo,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Notes:    "Reading is not changing: the method reads one volume and changes nothing.",
			ReadOnly: true,
		}, infoDoc()),
		Invoke: info,
		// Reading is the same in a check: Check is Invoke.
		Check: info,
	})
}

// info is container.volume.info's body: the volume named, or exists false
// when there is none. Its driver options are reported by name only, since
// a local volume's mount options can carry a password.
func info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	name, err := validName(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnInfo, err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnInfo, containerengine.OpVolumeInspect)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	live, exists, err := inspectVolume(ctx, s, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnInfo, err)
	}
	if err := recordStats(rc, map[string]any{statName: name, statExists: exists}); err != nil {
		return collection.Result{}, err
	}
	if !exists {
		return collection.Result{}, nil
	}
	labels := maps.Clone(live.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	return collection.Result{}, recordStats(rc, map[string]any{
		statDriver: live.Driver, statMountpoint: live.Mountpoint, statScope: live.Scope, statCreatedAt: live.CreatedAt,
		statLabels: labels, statOptionNames: slices.Sorted(maps.Keys(live.Options)),
	})
}

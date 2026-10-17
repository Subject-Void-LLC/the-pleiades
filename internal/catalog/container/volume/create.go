// container.volume.create: make a named volume exist as the task declares
// it, through one body its check shares.
package volume

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	moby "github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnCreate = "container.volume.create"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnCreate,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that created the volume records its removal, pinned to the moment the engine made it; one that found it as declared records nothing.",
			Inverses:   []sdk.InverseSpec{{FQCN: fqcnRemove, Record: []string{paramName, paramCreatedAt, containerengine.ParamEngine}}},
		}, createDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return create(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return create(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// declared is a volume as a task declares it.
type declared struct {
	driver string
	labels map[string]string
}

// parseDeclared reads a volume's declaration.
func parseDeclared(params map[string]any) (declared, error) {
	driver, err := containerengine.TextParam(params, paramDriver)
	if err != nil {
		return declared{}, err
	}
	d := declared{driver: driver}
	if d.driver == "" {
		d.driver = driverLocal
	}
	if !slices.Contains(drivers, d.driver) {
		return d, fmt.Errorf("%s: %q is not one of %v", paramDriver, d.driver, drivers)
	}
	labels, err := containerengine.StringMapParam(params, paramLabels)
	if err != nil {
		return d, err
	}
	d.labels = labels
	return d, nil
}

// differences names what the live volume lacks of the declaration. Labels
// the task does not name are allowed beside the declared ones.
func (d declared) differences(live moby.Volume) []string {
	var out []string
	if live.Driver != d.driver {
		out = append(out, fmt.Sprintf("%s: %s, the task names %s", paramDriver, live.Driver, d.driver))
	}
	for _, k := range slices.Sorted(maps.Keys(d.labels)) {
		if v, ok := live.Labels[k]; !ok || v != d.labels[k] {
			out = append(out, fmt.Sprintf("%s: %s is %q, the task names %q", paramLabels, k, v, d.labels[k]))
		}
	}
	return out
}

// create is container.volume.create's body: an absent volume is made; one
// that matches is no change; one that differs is refused, and never
// replaced, since replacing a volume loses its data.
func create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnCreate, err)
	}
	name, err := validName(params)
	if err != nil {
		return fail(err)
	}
	want, err := parseDeclared(params)
	if err != nil {
		return fail(err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnCreate, containerengine.OpVolumeInspect, containerengine.OpVolumeCreate)
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
	if exists {
		if err := recordStats(rc, map[string]any{statDriver: live.Driver, statCreatedAt: live.CreatedAt}); err != nil {
			return collection.Result{}, err
		}
		if diffs := want.differences(live); len(diffs) > 0 {
			return fail(fmt.Errorf("volume %s differs from the task, and is not replaced here, since that would lose its data:\n  %s", name, strings.Join(diffs, "\n  ")))
		}
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statDriver, want.driver)
	}
	made, err := s.API.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Driver: want.driver, Labels: want.labels})
	if err != nil {
		return fail(fmt.Errorf("creating volume %s: %w", name, err))
	}
	if made.Volume.Name != name {
		return fail(fmt.Errorf("the engine answered volume %q when asked to create %s", made.Volume.Name, name))
	}
	undo := map[string]any{paramName: name}
	if made.Volume.CreatedAt != "" {
		undo[paramCreatedAt] = made.Volume.CreatedAt
	}
	if err := containerengine.RecordUndo(rc, s, sdk.Inverse{FQCN: fqcnRemove, Params: undo, Description: "Remove the volume this task created."}); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, recordStats(rc, map[string]any{statDriver: made.Volume.Driver, statCreatedAt: made.Volume.CreatedAt})
}

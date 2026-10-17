// container.network.create: make a network exist as the task declares it,
// through one body its check shares.
package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnCreate = "container.network.create"

// Returns of create and info.
const (
	statDriver   = "driver"
	statInternal = "internal"
	statIPv6     = "enable_ipv6"
	statSubnets  = "subnets"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnCreate,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that created the network records its removal, by ID; one that found it as declared records nothing.",
			Inverses:   []sdk.InverseSpec{{FQCN: fqcnRemove, Record: []string{paramName, paramID, containerengine.ParamEngine}}},
		}, createDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return create(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return create(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// create is container.network.create's body: an absent network is made; one
// that matches the declaration is no change; one that differs is refused,
// naming each difference, and never replaced.
func create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnCreate, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := validNetworkName(name); err != nil {
		return fail(err)
	}
	want, err := parseSettings(params)
	if err != nil {
		return fail(err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnCreate, containerengine.OpNetworkInspect, containerengine.OpNetworkCreate)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	if err := rc.SetStat(statName, name); err != nil {
		return collection.Result{}, err
	}
	live, exists, err := inspectNetwork(ctx, s, name)
	if err != nil {
		return fail(err)
	}
	if exists {
		if err := recordStats(rc, map[string]any{statID: live.ID, statDriver: live.Driver}); err != nil {
			return collection.Result{}, err
		}
		if diffs := want.differences(live); len(diffs) > 0 {
			return fail(fmt.Errorf("network %s differs from the task, and is not replaced here; container.network.remove it first (its containers disconnected) to make it again:\n  %s", name, strings.Join(diffs, "\n  ")))
		}
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statDriver, want.Driver)
	}
	made, err := s.API.NetworkCreate(ctx, name, want.create())
	if err != nil {
		// A create whose answer was lost may have made the network: say so
		// rather than guess whether this run made it.
		cctx, cancel := containerengine.CleanupContext(ctx)
		defer cancel()
		if _, there, inspectErr := inspectNetwork(cctx, s, name); inspectErr == nil && there {
			return fail(fmt.Errorf("creating network %s: %w; a network by that name exists now, so run the task again to compare it", name, err))
		}
		return fail(fmt.Errorf("creating network %s: %w", name, err))
	}
	if !engineID.MatchString(made.ID) {
		return fail(fmt.Errorf("the engine answered an unusable ID for the network it created"))
	}
	if err := containerengine.RecordUndo(rc, s, sdk.Inverse{
		FQCN:        fqcnRemove,
		Params:      map[string]any{paramName: name, paramID: made.ID},
		Description: "Remove the network this task created.",
	}); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, recordStats(rc, map[string]any{statID: made.ID, statDriver: want.Driver})
}

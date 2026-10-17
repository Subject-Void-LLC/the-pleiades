// container.network.create: make a network exist as the task declares it,
// through one body its check shares.
package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

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
	// The create carries a label no other request has, so a create whose
	// answer was lost finds the network it made, and only that one.
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return collection.Result{}, err
	}
	attempt := hex.EncodeToString(nonce)
	opts := want.create()
	opts.Labels = maps.Clone(opts.Labels)
	if opts.Labels == nil {
		opts.Labels = map[string]string{}
	}
	opts.Labels[containerengine.LabelAttempt] = attempt
	made, err := s.API.NetworkCreate(ctx, name, opts)
	if err != nil {
		if cerrdefs.IsConflict(err) {
			return fail(fmt.Errorf("creating network %s: %w; another run made a network of that name meanwhile, so run the task again to compare it", name, err))
		}
		// The engine may have made it anyway. A failed task's undo is
		// dropped (FAILURE_PATTERNS 452), so this run's network is removed
		// here; another's is left alone.
		cctx, cancel := containerengine.CleanupContext(ctx)
		defer cancel()
		if live, there, inspectErr := inspectNetwork(cctx, s, name); inspectErr == nil && there && live.Labels[containerengine.LabelAttempt] == attempt {
			if _, rmErr := s.API.NetworkRemove(cctx, live.ID, client.NetworkRemoveOptions{}); rmErr == nil {
				return fail(fmt.Errorf("creating network %s: %w; the engine had made it anyway, and it was removed", name, err))
			}
			return fail(fmt.Errorf("creating network %s: %w; the engine made it anyway, and removing it failed, so container.network.remove it", name, err))
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

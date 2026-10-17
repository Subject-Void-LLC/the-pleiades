// container.network.disconnect: a container taken off a network, its endpoint kept for the undo.
package network

import (
	"context"
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnDisconnect,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that disconnected the container records connecting it again, by both IDs, with the aliases and static IPv4 address it had; one that found it not connected records nothing.",
			Inverses:   []sdk.InverseSpec{{FQCN: fqcnConnect, Record: append(slices.Clone(guards), paramAliases, paramIPv4, containerengine.ParamEngine)}},
		}, disconnectDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return disconnect(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return disconnect(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// disconnect is container.network.disconnect's body: a container on the
// network leaves it; one not on it, or a network or container that does
// not exist, is no change.
func disconnect(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnDisconnect, err)
	}
	network, container, err := names(params)
	if err != nil {
		return fail(err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnDisconnect,
		containerengine.OpNetworkInspect, containerengine.OpContainerInspect, containerengine.OpNetworkDisconnect)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	p, present, err := read(ctx, rc, s, params, network, container)
	if err != nil {
		return fail(err)
	}
	if !present || p.endpoint == nil {
		return collection.Result{}, rc.SetStat(statConnected, false)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statConnected, false)
	}
	if _, err := s.API.NetworkDisconnect(ctx, p.network.ID, client.NetworkDisconnectOptions{Container: p.container.ID}); err != nil {
		return fail(fmt.Errorf("disconnecting container %s from network %s: %w", container, network, err))
	}
	if err := containerengine.RecordUndo(rc, s, sdk.Inverse{
		FQCN:        fqcnConnect,
		Params:      reconnectParams(network, container, p),
		Description: "Connect the container this task disconnected again, as it was.",
	}); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, rc.SetStat(statConnected, false)
}

// reconnectParams are a connect that puts the container back on the
// network as it was: its aliases and its static IPv4 address, when it had
// them.
func reconnectParams(network, container string, p pair) map[string]any {
	out := map[string]any{paramNetwork: network, paramContainer: container, paramNetworkID: p.network.ID, paramContainerID: p.container.ID}
	if len(p.endpoint.Aliases) > 0 {
		out[paramAliases] = slices.Clone(p.endpoint.Aliases)
	}
	if cfg := p.endpoint.IPAMConfig; cfg != nil && cfg.IPv4Address.IsValid() {
		out[paramIPv4] = cfg.IPv4Address.String()
	}
	return out
}

// container.network.remove: remove a network no container uses, through
// one body its check shares.
package network

import (
	"context"
	"fmt"
	"slices"
	"strings"

	moby "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnRemove = "container.network.remove"

// statRemoved says the network was removed, or by a check would be.
const statRemoved = "removed"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRemove,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that removed the network records creating it again with the settings it had: driver, internal, IPv6, its first subnet with its gateway and range, MTU, parent and labels. Labels are not kept in the journal. The undo is partial when the network had an option or a second subnet no parameter sets; a network is a fresh one with a new ID once made again, and the containers that used it are not reconnected.",
			Inverses: []sdk.InverseSpec{{
				FQCN:         fqcnCreate,
				Record:       []string{paramName, paramDriver, paramInternal, paramIPv6, paramSubnet, paramGateway, paramIPRange, paramMTU, paramParent},
				Withhold:     []string{paramLabels},
				MayBePartial: true,
			}},
		}, removeDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return remove(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// remove is container.network.remove's body: an absent network is no
// change; one any container uses, running or not, is refused.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRemove, err)
	}
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return fail(err)
	}
	if err := validNetworkName(name); err != nil {
		return fail(err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnRemove,
		containerengine.OpNetworkInspect, containerengine.OpContainerList, containerengine.OpNetworkRemove)
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
	if !exists {
		return collection.Result{}, nil
	}
	if err := guardID(params, paramID, "network", name, live.ID); err != nil {
		return fail(err)
	}
	if err := rc.SetStat(statID, live.ID); err != nil {
		return collection.Result{}, err
	}
	users, err := usersOf(ctx, s, live)
	if err != nil {
		return fail(err)
	}
	if len(users) > 0 {
		return fail(fmt.Errorf("network %s is used by %s; container.network.disconnect or remove them first", name, strings.Join(users, ", ")))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statRemoved, true)
	}
	if _, err := s.API.NetworkRemove(ctx, live.ID, client.NetworkRemoveOptions{}); err != nil {
		return fail(fmt.Errorf("network %s: %w", name, err))
	}
	undo, partial := recreateParams(live)
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        fqcnCreate,
		Params:      undo,
		Description: "Create the network this task removed again, with the settings it had.",
		Partial:     partial,
	}); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, rc.SetStat(statRemoved, true)
}

// usersOf names the containers, running or stopped, that use the network:
// a stopped one declaring it would fail to start once it is gone. The
// filter names the network both ways, since an engine matches a container
// that has never run by the network's name alone, and the two values are
// either-or.
func usersOf(ctx context.Context, s *containerengine.Session, live moby.Inspect) ([]string, error) {
	res, err := s.API.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("network", live.Name, live.ID)})
	if err != nil {
		return nil, fmt.Errorf("listing the containers using network %s: %w", live.Name, err)
	}
	var names []string
	for _, c := range res.Items {
		name := c.ID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

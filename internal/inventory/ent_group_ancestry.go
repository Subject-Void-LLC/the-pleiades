package inventory

import (
	"context"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
)

// GroupAncestry implements Repository.GroupAncestry: the first real
// traversal of Group.parents/children in this codebase. Before Phase 72,
// GetGroup's own doc comment recorded why that traversal did not exist
// yet ("nothing in this codebase populates those edges..., building
// traversal for a case with zero live callers would be exactly the kind
// of premature infrastructure this project's own conventions avoid
// elsewhere"). A device's configured bastion/hop-chain route, resolved
// through pkg/policy.Resolve at System -> Inventory -> Group -> Device,
// is that first real caller.
//
// The walk is a breadth-first search from deviceName's own direct groups,
// outward through Group.parents. Both the Group DAG (a group can have
// more than one parent) and group/inventory membership (a group can
// belong to more than one inventory) are genuinely graph-shaped, not
// tree-shaped, so this returns a deterministic ordering rather than
// claiming there is one true traversal:
//
//   - Every Inventory reached, however it was reached (a group's own
//     inventories edge, at any distance, or the device's own direct
//     inventories edge), is ONE layer, least specific of all: Inventory
//     sits above every Group in the System -> Inventory -> Group ->
//     Device hierarchy regardless of how many group levels separate a
//     device from it.
//   - Every Group reached is ordered by its BFS distance from the
//     device, farthest (least specific) first, nearest (most specific)
//     last. A group reachable at more than one distance (a DAG can do
//     this: directly AND via a longer path through another group) is
//     recorded at the SHORTEST distance it is reachable at, since BFS
//     visits in non-decreasing distance order and a node is recorded
//     the first time it is seen; the shortest path is the most specific
//     one honestly available.
//   - Groups (and Inventories) at the identical distance are siblings
//     with no natural order between them; the tiebreak is name,
//     ascending, so the result is deterministic across runs rather than
//     depending on map iteration order.
func (r *entRepository) GroupAncestry(ctx context.Context, deviceName string) ([]HierarchyLayer, error) {
	client := r.entClient(ctx)

	dev, err := client.Device.Query().Where(device.NameEQ(deviceName)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("device %s: %w", deviceName, ErrItemNotFound)
		}
		return nil, fmt.Errorf("failed to load device %s: %w", deviceName, err)
	}

	directGroups, err := dev.QueryGroups().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load groups for device %s: %w", deviceName, err)
	}
	directInventories, err := dev.QueryInventories().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load inventories for device %s: %w", deviceName, err)
	}

	visitedGroups := map[int]*ent.Group{}
	groupDistance := map[int]int{}
	visitedInventories := map[int]*ent.Inventory{}
	for _, inv := range directInventories {
		visitedInventories[inv.ID] = inv
	}

	frontier := directGroups
	for distance := 0; len(frontier) > 0; distance++ {
		var next []*ent.Group
		for _, g := range frontier {
			if _, seen := visitedGroups[g.ID]; seen {
				continue
			}
			visitedGroups[g.ID] = g
			groupDistance[g.ID] = distance

			invs, err := g.QueryInventories().All(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to load inventories for group %s: %w", g.Name, err)
			}
			for _, inv := range invs {
				visitedInventories[inv.ID] = inv
			}

			parents, err := g.QueryParents().All(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to load parent groups for group %s: %w", g.Name, err)
			}
			next = append(next, parents...)
		}
		frontier = next
	}

	var layers []HierarchyLayer

	inventoryLayer := make([]*ent.Inventory, 0, len(visitedInventories))
	for _, inv := range visitedInventories {
		inventoryLayer = append(inventoryLayer, inv)
	}
	sort.Slice(inventoryLayer, func(i, j int) bool { return inventoryLayer[i].Name < inventoryLayer[j].Name })
	for _, inv := range inventoryLayer {
		layers = append(layers, HierarchyLayer{Name: inv.Name, Properties: inv.Properties})
	}

	maxDistance := -1
	for _, d := range groupDistance {
		if d > maxDistance {
			maxDistance = d
		}
	}
	for d := maxDistance; d >= 0; d-- {
		var atDistance []*ent.Group
		for id, gd := range groupDistance {
			if gd == d {
				atDistance = append(atDistance, visitedGroups[id])
			}
		}
		sort.Slice(atDistance, func(i, j int) bool { return atDistance[i].Name < atDistance[j].Name })
		for _, g := range atDistance {
			layers = append(layers, HierarchyLayer{Name: g.Name, Properties: g.Properties})
		}
	}

	return layers, nil
}

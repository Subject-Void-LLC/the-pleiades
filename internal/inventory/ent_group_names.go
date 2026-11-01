// Package inventory: every device's group names, its groups' ancestors
// included, for a limit that names a group (engine.ParseLimit).
package inventory

import (
	"context"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/group"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// GroupNamer is implemented by a Repository that can say which groups each
// device is in, through any ancestor: a device in a child group is in its
// parent, as an Ansible inventory's group is. A fan-out asks once per job
// rather than once per device.
type GroupNamer interface {
	DeviceGroupNames(ctx context.Context) (map[inventory.DeviceID][]string, error)
}

// DeviceGroupNames implements GroupNamer: one query of every group with its
// devices and parents, then each group's ancestors walked with a guard
// against a cycle a hand-edited database could hold.
func (r *entRepository) DeviceGroupNames(ctx context.Context) (map[inventory.DeviceID][]string, error) {
	groups, err := r.entClient(ctx).Group.Query().
		WithDevices(func(q *ent.DeviceQuery) { q.Select(device.FieldDeviceID) }).
		WithParents(func(q *ent.GroupQuery) { q.Select(group.FieldID) }).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read the inventory's groups: %w", err)
	}
	name := map[int]string{}
	parents := map[int][]int{}
	for _, g := range groups {
		name[g.ID] = g.Name
		for _, p := range g.Edges.Parents {
			parents[g.ID] = append(parents[g.ID], p.ID)
		}
	}
	// ancestry returns g and every group above it.
	ancestry := func(g int) []string {
		seen := map[int]bool{}
		var out []string
		var walk func(int)
		walk = func(id int) {
			if seen[id] {
				return
			}
			seen[id] = true
			out = append(out, name[id])
			for _, p := range parents[id] {
				walk(p)
			}
		}
		walk(g)
		return out
	}
	byDevice := map[inventory.DeviceID]map[string]bool{}
	for _, g := range groups {
		names := ancestry(g.ID)
		for _, d := range g.Edges.Devices {
			id := inventory.DeviceID(d.DeviceID)
			if byDevice[id] == nil {
				byDevice[id] = map[string]bool{}
			}
			for _, n := range names {
				byDevice[id][n] = true
			}
		}
	}
	out := make(map[inventory.DeviceID][]string, len(byDevice))
	for id, set := range byDevice {
		for n := range set {
			out[id] = append(out[id], n)
		}
		sort.Strings(out[id])
	}
	return out, nil
}

// This file is the inventory's membership controls: which devices and
// groups it contains, offered as choosers rather than as numbers to type.
//
// An inventory stores its membership as numeric ids. Before these controls
// existed the edit form carried neither list, so the writer had to copy
// both back from storage on every save just to avoid clearing them, and an
// inventory created through the UI could never be filled through the UI. A
// chooser is not a nicety here: a mistyped id silently shares the wrong
// hosts with the wrong team, and nothing about the stored result looks
// wrong afterwards.
package inventories

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// deviceOptions offers the fleet by name.
func deviceOptions(sets inventory.SetStore) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		members, err := sets.ListMembers(ctx, 0)
		if err != nil {
			return nil, err
		}
		return options(members.Devices, members.Truncated), nil
	}
}

// groupOptions offers the device groups by name.
func groupOptions(sets inventory.SetStore) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		members, err := sets.ListMembers(ctx, 0)
		if err != nil {
			return nil, err
		}
		return options(members.Groups, members.Truncated), nil
	}
}

// options projects members onto chooser entries.
//
// A truncated list says so in the last entry rather than ending silently.
// The entry carries no value, so choosing it selects nothing: it is a label
// in a control that has no other place to put one, which is the honest
// version of a chooser that cannot reach every host in a large estate. What
// such a deployment needs is a search, which view.Filter would be and is
// not built.
func options(members []inventory.Member, truncated bool) []view.Option {
	out := make([]view.Option, 0, len(members)+1)
	for _, m := range members {
		out = append(out, view.Option{Label: m.Name, Value: strconv.Itoa(m.ID)})
	}
	if truncated {
		out = append(out, view.Option{Label: "(more exist than this control can list)", Value: ""})
	}
	return out
}

// joinIDs renders a membership list for the form control, comma separated
// because that is what FormModel.IsSelected splits on.
func joinIDs(ids []int) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	return strings.Join(parts, ",")
}

// parseIDs reads a submitted membership back.
//
// Anything unparseable is dropped rather than reported. The values come
// from a chooser this view rendered, so a non-numeric one did not come from
// the form, and the empty value the truncation notice carries is exactly
// such a case: it exists to be skipped here.
func parseIDs(values []string) []int {
	out := make([]int, 0, len(values))
	for _, raw := range values {
		id, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || id < 1 {
			continue
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// membersSection is the inventory's Hosts tab: which devices it names
// directly.
//
// This was a view.Planned panel whose stated reason was that "this view
// holds no port to resolve them into devices". That is no longer true;
// ListMembers resolves an id to a name, which is the whole of what a
// listing needs.
//
// It reports only the DIRECT devices, and says so, because that is the only
// membership this view can answer honestly. Devices reached through a group
// need the group ancestry walk in internal/inventory, which resolves a DAG
// with multiple parents and is not something to reimplement in a panel.
func membersSection(sets inventory.SetStore) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Devices",
		Summary: "The devices this inventory names directly. Devices reached through one of its groups are not listed here.",
		Empty: "No device is attached to this inventory directly, so a dispatch against it reaches nothing " +
			"unless one of its groups contains something. Add devices from this inventory's edit form.",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true, References: "devices"},
			{Name: "id", Label: "ID", Kind: view.KindReadOnly, InList: true},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			if strings.TrimSpace(parentID) == "" {
				return nil, nil
			}
			id, err := strconv.Atoi(parentID)
			if err != nil {
				return nil, nil
			}
			set, err := sets.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			if len(set.DeviceIDs) == 0 {
				return nil, nil
			}

			members, err := sets.ListMembers(ctx, 0)
			if err != nil {
				return nil, err
			}
			byID := make(map[int]string, len(members.Devices))
			for _, m := range members.Devices {
				byID[m.ID] = m.Name
			}

			rows := make([]view.Row, 0, len(set.DeviceIDs))
			for _, deviceID := range set.DeviceIDs {
				name, known := byID[deviceID]
				if !known {
					// A referenced device the chooser could not list. Shown
					// rather than dropped: a membership pointing at a device
					// this control cannot see is exactly the row somebody
					// auditing an inventory needs to find.
					name = "(not listed)"
				}
				rows = append(rows, view.Row{
					ID:    strconv.Itoa(deviceID),
					Cells: view.Cells{"name": name, "id": strconv.Itoa(deviceID)},
					Refs:  map[string]string{"name": name},
				})
			}
			return rows, nil
		},
	}
}

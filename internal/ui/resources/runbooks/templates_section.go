// The templates that run a given runbook: the missing half of the one
// relationship this view has.
//
// A runbook is a file and cannot be dispatched on its own, because a dispatch
// needs somewhere to run and a runbook names no inventory. A template is what
// binds the two, and it is the only launchable thing in this platform. That
// made the relationship one-way in the interface: a template names its runbook
// and links to it, and a runbook had no way to answer "what runs me", so the
// path from a runbook to actually running it went through the Templates list
// and a search.
package runbooks

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// sectionLimit bounds one section's read.
//
// A section is a drill-down rather than a listing: somebody who needs every
// template in a large deployment goes to the Templates view, which pages.
const sectionLimit = 200

// templatesSection is what already runs this runbook.
//
// Each row links to the template's own record, which is where Launch lives.
// That is the answer to "how do I run this": not a button here, because a
// dispatch needs an inventory this page has no way to choose, but one click to
// the thing that already has one.
func templatesSection(store launch.Store) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Templates",
		Summary: "The saved definitions that run this runbook. Launching happens on a template, because a dispatch needs an inventory and a runbook names none.",
		Empty: "No template runs this runbook yet, so nothing can dispatch it. " +
			"Create one from this page and it becomes launchable against whichever inventory it names.",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true, References: "templates"},
			{Name: "inventory", Label: "INVENTORY", Kind: view.KindText, InList: true},
			{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText, InList: true},
			{Name: "kind", Label: "KIND", Kind: view.KindText, InList: true},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			if strings.TrimSpace(parentID) == "" {
				// A section under no record must not answer with every
				// template in the deployment.
				return nil, nil
			}

			// launch.Store pages by name with no by-definition query, so the
			// filter happens here. Honest at this size and wrong at a larger
			// one, which is why the cap is a stated constant rather than a
			// number buried in a call: a deployment that outgrows it wants a
			// real ListForDefinition, not a bigger number here.
			found, err := store.List(ctx, launch.Query{Limit: sectionLimit})
			if err != nil {
				return nil, err
			}

			rows := make([]view.Row, 0, 4)
			for _, tmpl := range found {
				if tmpl.Definition != parentID {
					continue
				}
				rows = append(rows, view.Row{
					ID: strconv.Itoa(tmpl.ID),
					Cells: view.Cells{
						"name":         tmpl.Name,
						"inventory":    tmpl.InventoryName,
						"organization": tmpl.OrganizationName,
						"kind":         tmpl.KindName,
					},
					Refs: map[string]string{"name": strconv.Itoa(tmpl.ID)},
				})
			}
			return rows, nil
		},
	}
}

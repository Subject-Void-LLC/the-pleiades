// Package inventories is the Inventories view resource: the named,
// shareable sets of devices a runbook is dispatched against.
//
// This is the container layer, not the device list. The device list moved
// to its own view when this one arrived, because the word was doing two
// jobs: an operator asking "which inventories can I run against" and one
// asking "is core-router-01 healthy" are asking different questions, and one
// page answering both answers neither well. Organization -> Inventory ->
// Group -> Device is the containment order, and it is the same one AWX uses.
//
// Sharing has no controls here and no fields, deliberately. Lending an
// inventory to another team is a RoleBinding at inventory scope, resolved by
// the same hierarchical chain that governs organizations, groups and
// devices. A second sharing mechanism with its own UI would be a second
// place for the two to disagree about who can reach what.
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package inventories

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "inventories"

// fields drive the table, the form, the detail list, validation and the
// mobile card layout from one declaration.
func fields(sets inventory.SetStore) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, Autocomplete: "off",
			Help:          "What this set of devices is called. Unique within its organization, so two tenants may both have a \"production\".",
			InList:        true,
			InForm:        true,
			MobilePrimary: true,
		},
		{
			Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
			MaxLen: 1024, InForm: true, InList: true,
			Help: "What it is for, for somebody who did not create it.",
		},
		{
			Name: "organization", Label: "ORGANIZATION", Kind: view.KindSelect,
			Required: true, Immutable: true, InForm: true, InList: true,
			References: "organizations",
			// A select rather than a number field: an inventory must name
			// an organization, and a form asking somebody to type a numeric
			// primary key is a form nobody can fill in.
			//
			// Immutable because Update reads it from storage regardless, for
			// the reason that method gives: moving an inventory between
			// tenants silently re-scopes every RoleBinding pointing at it,
			// which is a migration rather than an edit. Offering the control
			// anyway made that a decision somebody could take, submit and be
			// told had succeeded.
			Options: func(ctx context.Context) ([]view.Option, error) {
				orgs, err := sets.ListOrganizations(ctx)
				if err != nil {
					return nil, err
				}
				out := make([]view.Option, 0, len(orgs))
				for _, org := range orgs {
					out = append(out, view.Option{Label: org.Name, Value: strconv.Itoa(org.ID)})
				}
				return out, nil
			},
		},
		{
			Name: "groups", Label: "GROUPS", Kind: view.KindReadOnly, InList: true,
			Help: "How many device groups this inventory contains.",
		},
		{
			Name: "devices", Label: "DIRECT DEVICES", Kind: view.KindReadOnly, InList: true,
			Help: "Devices attached with no intervening group. Devices reached through a group are not counted here.",
		},
		{
			Name: "owner", Label: "CREATED BY", Kind: view.KindReadOnly,
			Help: "Authorship, never authority: creating an inventory grants no permission over it.",
		},
	}
}

// reader adapts the SetStore's read half.
type reader struct{ sets inventory.SetStore }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[inventory.Set], error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	after := 0
	if q.Cursor != "" {
		parsed, err := strconv.Atoi(q.Cursor)
		if err != nil {
			// A malformed cursor is treated as no cursor rather than as an
			// error: the worst outcome is the first page, which is a page
			// that works.
			parsed = 0
		}
		after = parsed
	}

	// One more than asked for, so a next page is observed rather than
	// inferred from a page that happened to come back full.
	found, err := r.sets.List(ctx, inventory.SetQuery{
		After:  after,
		Limit:  limit + 1,
		Search: q.Search,
	})
	if err != nil {
		return view.Page[inventory.Set]{}, err
	}

	page := view.Page[inventory.Set]{Items: found}
	if len(found) > limit {
		page.Items = found[:limit]
		page.NextCursor = strconv.Itoa(found[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (inventory.Set, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return inventory.Set{}, inventory.ErrSetNotFound
	}
	return r.sets.Get(ctx, numeric)
}

// writer adapts the write half.
type writer struct{ sets inventory.SetStore }

func (w writer) Create(ctx context.Context, set inventory.Set) (string, error) {
	created, err := w.sets.Create(ctx, set)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(created.ID), nil
}

func (w writer) Update(ctx context.Context, id string, set inventory.Set) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return inventory.ErrSetNotFound
	}

	// The organization is read from storage rather than from the
	// submission: moving an inventory between tenants would silently
	// re-scope every RoleBinding pointing at it, which is a migration
	// rather than an edit.
	existing, err := w.sets.Get(ctx, numeric)
	if err != nil {
		return err
	}
	set.ID = numeric
	set.OrganizationID = existing.OrganizationID
	set.GroupIDs = existing.GroupIDs
	set.DeviceIDs = existing.DeviceIDs
	return w.sets.Update(ctx, set)
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return inventory.ErrSetNotFound
	}
	return w.sets.Delete(ctx, numeric)
}

// Register wires this view over the live set store.
func Register(sets inventory.SetStore) error {
	declared := fields(sets)

	projector := view.Projector[inventory.Set]{
		Row: func(set inventory.Set) view.Row {
			return view.Row{
				ID: strconv.Itoa(set.ID),
				// The id the cell links to; its name is in Cells.
				Refs: map[string]string{"organization": strconv.Itoa(set.OrganizationID)},
				Cells: view.Cells{
					"name":         set.Name,
					"description":  set.Description,
					"organization": set.OrganizationName,
					"groups":       strconv.Itoa(len(set.GroupIDs)),
					"devices":      strconv.Itoa(len(set.DeviceIDs)),
					"owner":        set.Owner,
				},
			}
		},
		Form: func(set inventory.Set) map[string]string {
			return map[string]string{
				"name":         set.Name,
				"description":  set.Description,
				"organization": strconv.Itoa(set.OrganizationID),
			}
		},
		Bind: func(v view.Values) (inventory.Set, view.FieldErrors) {
			errs := view.FieldErrors{}

			// The organization is Immutable, so an edit form never renders
			// it and an edit submission never carries it. Demanding one
			// here refused every edit, which is what this guard fixes; on
			// an edit Update reads the real value from storage, so leaving
			// it zero declines to overwrite rather than unsetting it.
			var org int
			if !v.Editing() {
				parsed, err := strconv.Atoi(strings.TrimSpace(v.Get("organization")))
				if err != nil || parsed < 1 {
					// Blamed on the field rather than answered with a 500. A
					// submission with no organization is the one thing the
					// store refuses outright, so catching it here is what turns
					// a refusal into a message next to the control.
					errs.Add("organization", "Choose the organization this inventory belongs to.")
					return inventory.Set{}, errs
				}
				org = parsed
			}

			return inventory.Set{
				Name:           v.Get("name"),
				Description:    v.Get("description"),
				OrganizationID: org,
			}, errs
		},
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Inventories",
		NavLabel: "INVENTORIES",
		// The fleet half of Resources, after the three objects that
		// describe work and before the devices this contains.
		NavOrder: 70,
		NavGroup: view.NavGroupResources,
		Summary:  "Named sets of devices a runbook can be dispatched against.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   declared,
		Ops: view.Ops{
			List:   &apispec.ListInventories,
			Get:    &apispec.GetInventory,
			Create: &apispec.CreateInventory,
			Update: &apispec.UpdateInventory,
			Delete: &apispec.DeleteInventory,
		},
		Handlers: view.MustBind(reader{sets}, writer{sets}, projector),
	})
}

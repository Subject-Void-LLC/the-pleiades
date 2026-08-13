// Package devices is the Devices view resource: the managed hosts
// themselves.
//
// It used to be registered as "inventories". That name moved to the
// container above it -- the shareable set a runbook is dispatched against --
// because the word was doing two jobs at once: "which inventories can I run
// against" and "is core-router-01 healthy" are different questions, and one
// page answering both answered neither well.
//
// This is what a resource costs: a field declaration, an adapter over a
// port that already exists, a projector, and a registration. No handler,
// no template, no route, no nav entry, no CSS.
//
// It is reachable only because internal/ui/resources/builtins.go
// blank-imports it. An init() nothing imports never runs, and a package
// that compiles and passes its own tests while being invisible to the
// running binary is the failure recorded as FAILURE_PATTERNS.md #52.
package devices

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Name is this view's registration key and URL segment.
const Name = "devices"

// fields is the one declaration driving the table, the form, the detail
// list, server-side validation, and the mobile card layout.
var fields = []view.Field{
	{
		Name: "name", Label: "NAME", Kind: view.KindText,
		Required: true, MaxLen: 253, Autocomplete: "off",
		Help:          "The device's unique name, as the inventory knows it.",
		InList:        true,
		InForm:        true,
		Sortable:      true,
		MobilePrimary: true,
	},
	{
		Name: "type", Label: "TYPE", Kind: view.KindText,
		Required: true, MaxLen: 64, Autocomplete: "off",
		Help:   "A registered device type, for example linux_server.",
		InList: true, InForm: true,
	},
	{
		Name: "state", Label: "STATE", Kind: view.KindBadge,
		InList: true, BadgeClass: stateBadge,
	},
	{Name: "tags", Label: "TAGS", Kind: view.KindTags, InList: true, InForm: true},
	{Name: "version", Label: "VERSION", Kind: view.KindReadOnly},
}

// stateBadge maps a lifecycle state onto one of the closed set of badge
// classes. A value outside that set would be a caller-controlled string
// reaching a class attribute, which the content security policy exists to
// make impossible, so the default is neutral rather than the raw value.
func stateBadge(state string) string {
	switch state {
	case pkginventory.StateActive.String():
		return "badge-ok"
	case pkginventory.StateQuarantined.String(), pkginventory.StateUnreachable.String():
		return "badge-failed"
	case pkginventory.StateArchived.String(), pkginventory.StateDecommissioning.String():
		return "badge-skipped"
	case pkginventory.StateDiscovered.String(), pkginventory.StateOnboarding.String():
		return "badge-changed"
	default:
		return "badge-neutral"
	}
}

// reader adapts the inventory.Repository port to the view's Reader.
type reader struct {
	repo    inventory.Repository
	factory *inventory.ItemFactory
}

func (r reader) List(ctx context.Context, q view.Query) (view.Page[pkginventory.InventoryItem], error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	// One more than asked for, so a next page is observed rather than
	// guessed at from a full page.
	it, err := r.repo.GetGroup(ctx, pkginventory.Selector{
		After: pkginventory.DeviceID(q.Cursor),
		Limit: limit + 1,
	})
	if err != nil {
		return view.Page[pkginventory.InventoryItem]{}, err
	}
	defer func() { _ = it.Close() }()

	var page view.Page[pkginventory.InventoryItem]
	for it.Next(ctx) {
		if len(page.Items) == limit {
			page.NextCursor = string(page.Items[limit-1].ID())
			break
		}
		page.Items = append(page.Items, it.Item())
	}
	return page, it.Error()
}

func (r reader) Get(ctx context.Context, id string) (pkginventory.InventoryItem, error) {
	return r.repo.GetByName(ctx, id)
}

// writer adapts the same port's write half.
type writer struct {
	repo    inventory.Repository
	factory *inventory.ItemFactory
}

func (w writer) Create(ctx context.Context, item pkginventory.InventoryItem) (string, error) {
	if err := w.repo.Create(ctx, item); err != nil {
		return "", err
	}
	return item.Name(), nil
}

func (w writer) Update(ctx context.Context, id string, item pkginventory.InventoryItem) error {
	return w.repo.Save(ctx, item)
}

func (w writer) Delete(ctx context.Context, id string) error {
	// Retirement, not row removal: every Revision is immutable by schema
	// and the revisions edge carries no cascade, so a real delete would
	// mean destroying the audit trail the schema exists to protect.
	return w.repo.Retire(ctx, id)
}

// Register wires this view over a live repository.
//
// It takes its dependencies rather than reaching for a global, so the
// composition root stays the one place a concrete driver is chosen. That
// is also why this is a function rather than an init(): a view backed by a
// port needs the port, and there is none at package-initialisation time.
func Register(repo inventory.Repository, factory *inventory.ItemFactory) error {
	projector := view.Projector[pkginventory.InventoryItem]{
		Row: func(d pkginventory.InventoryItem) view.Row {
			return view.Row{ID: d.Name(), Cells: view.Cells{
				"name":    d.Name(),
				"type":    deviceType(d),
				"state":   d.State().String(),
				"tags":    joinTags(d.Tags()),
				"version": itoa(d.Version()),
			}}
		},
		Form: func(d pkginventory.InventoryItem) map[string]string {
			return map[string]string{
				"name": d.Name(),
				"type": deviceType(d),
				"tags": joinTags(d.Tags()),
			}
		},
		Bind: func(v view.Values) (pkginventory.InventoryItem, view.FieldErrors) {
			errs := view.FieldErrors{}
			tags := make([]pkginventory.Tag, 0)
			for _, t := range v.Tags("tags") {
				tags = append(tags, pkginventory.Tag(t))
			}
			item, err := factory.Build(record.Record{
				ID:     newDeviceID(),
				Name:   v.Get("name"),
				Type:   v.Get("type"),
				Tags:   tags,
				State:  pkginventory.StateActive,
				Source: pkginventory.SourceAuthority{Plugin: "ui"},
			})
			if err != nil {
				// The factory refuses a type no builtin registers, which
				// is the caller's mistake and belongs on the field that
				// carried it rather than as a 500.
				errs.Add("type", "That device type is not registered in this build.")
				return nil, errs
			}
			return item, errs
		},
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Devices",
		NavLabel: "DEVICES",
		// Directly after Inventories, because a device is what an inventory
		// contains and reading the two in that order is how the containment
		// actually runs.
		NavOrder: 80,
		NavGroup: view.NavGroupResources,
		Summary:  "Every device this control plane knows about.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   fields,
		Ops: view.Ops{
			List:   &apispec.ListDevices,
			Get:    &apispec.GetDevice,
			Create: &apispec.CreateDevice,
			Update: &apispec.UpdateDevice,
			Delete: &apispec.DeleteDevice,
		},
		Applies:  applies,
		Handlers: view.MustBind(reader{repo, factory}, writer{repo, factory}, projector),
	})
}

// applies withdraws the delete affordance from an already-archived device,
// regardless of how broadly the caller is scoped. Retirement is idempotent
// at the repository, so following such a link would succeed and change
// nothing -- a user would take an action and watch nothing happen, with no
// error to explain why.
func applies(row view.Row, rel auth.LinkRel) bool {
	if rel == auth.RelDelete {
		return row.Cells["state"] != pkginventory.StateArchived.String()
	}
	return true
}

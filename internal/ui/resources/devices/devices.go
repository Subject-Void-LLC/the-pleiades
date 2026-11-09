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
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
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
		// Fixed once the device exists. The name is the device's address
		// (/ui/devices/{name}) and neither repository's Save writes a new
		// one, so an edit form offering it would take a rename and store
		// nothing.
		Immutable:     true,
		Help:          "The device's unique name, as the inventory knows it. Fixed once saved.",
		InList:        true,
		InForm:        true,
		Sortable:      true,
		MobilePrimary: true,
	},
	{
		Name: "type", Label: "TYPE", Kind: view.KindText,
		Required: true, MaxLen: 64, Autocomplete: "off",
		// Fixed once the device exists: the stored type column is
		// immutable, and the type decides what the device can do.
		Immutable: true,
		Help:      "A registered device type, for example linux_server. Fixed once saved.",
		InList:    true, InForm: true,
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

// Register wires this view over a live repository.
//
// It takes its dependencies rather than reaching for a global, so the
// composition root stays the one place a concrete driver is chosen. That
// is also why this is a function rather than an init(): a view backed by a
// port needs the port, and there is none at package-initialisation time.
func Register(repo inventory.Repository, factory *inventory.ItemFactory) error {
	projector := view.Projector[device]{
		Row: func(d device) view.Row {
			item := d.item
			return view.Row{ID: item.Name(), Cells: view.Cells{
				"name":    item.Name(),
				"type":    deviceType(item),
				"state":   item.State().String(),
				"tags":    joinTags(item.Tags()),
				"version": itoa(item.Version()),
			}}
		},
		// Form prefills the edit form, which offers the tags alone: the
		// name and type are Immutable, so the form renders no control for
		// either and a prefill naming them would be refused.
		Form: func(d device) map[string]string {
			return map[string]string{"tags": joinTags(d.item.Tags())}
		},
		Bind: func(v view.Values) (device, view.FieldErrors) {
			errs := view.FieldErrors{}
			tags := make([]pkginventory.Tag, 0)
			for _, t := range v.Tags("tags") {
				tags = append(tags, pkginventory.Tag(t))
			}
			// An edit carries the tags and nothing else; the writer applies
			// them to the stored device. Building a device here, as a
			// create does, is what made every edit store nothing
			// (FAILURE_PATTERNS 1044).
			if v.Editing() {
				return device{tags: tags}, errs
			}
			// A device's name is its address (/ui/devices/{name}), so one no
			// URL can carry is refused on the field, with the reason, before
			// anything is built or stored. The shared validator answers an
			// empty name first, since the field is required; recordname
			// adds "." and "..".
			if err := recordname.Check(v.Get("name")); err != nil {
				errs.Add("name", err.Error())
				return device{}, errs
			}
			item, err := factory.Build(record.Record{
				ID:   newDeviceID(),
				Name: v.Get("name"),
				Type: v.Get("type"),
				Tags: tags,
				// Active, or discovered for a type only onboarding makes
				// active.
				State:  record.InitialState(v.Get("type")),
				Source: pkginventory.SourceAuthority{Plugin: "ui"},
			})
			if err != nil {
				// The factory refuses a type no builtin registers, which
				// is the caller's mistake and belongs on the field that
				// carried it rather than as a 500. A registered type can
				// refuse too, when it needs a property this form has no
				// field for (generic_http's base_url, generic_grpc's
				// target), and saying "not registered" then would send
				// the person looking for the wrong problem.
				if _, registered := record.LookupType(v.Get("type")); registered {
					errs.Add("type", "That device type needs properties this form cannot set; add it with pleiades add-host or a sync.")
				} else {
					errs.Add("type", "That device type is not registered in this build.")
				}
				return device{}, errs
			}
			return device{item: item}, errs
		},
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Devices",
		NavLabel: "DEVICES",
		// Directly after Inventories, because a device is what an inventory
		// contains and reading the two in that order is how the containment
		// actually runs.
		NavOrder:         80,
		NavGroup:         view.NavGroupResources,
		Summary:          "Every device this control plane knows about.",
		Status:           view.StatusImplemented,
		IDField:          "name",
		StatusBadgeField: "state",
		// AWX's host tabs, named for this platform's own vocabulary: a
		// managed machine is a device here and a named set of them is an
		// inventory, so AWX's "Groups" is "Inventories".
		//
		// Access is declared rather than implemented even though a device
		// IS a grant target: bindings key on a numeric device id and this
		// view addresses a device by its name, so the section would have
		// filtered to nothing and rendered "no grants name this device" on
		// every page, which is a wrong answer rather than a missing one.
		Sections: []view.Section{
			view.Planned("Inventories",
				"The inventories this device belongs to, and therefore what may be dispatched against it.",
				"Membership is resolved by numeric device id and this view addresses a device by name, so the lookup this needs is not wired here yet.",
				[]view.Field{
					{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true, References: "inventories"},
					{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText, InList: true, References: "organizations"},
					{Name: "via", Label: "MEMBER BY", Kind: view.KindText, InList: true},
				}),
			view.Planned("Facts",
				"What this device last reported about itself.",
				"Gathered facts are returned to the job that asked for them and are not stored, so there is nothing to read back between runs.",
				[]view.Field{
					{Name: "fact", Label: "FACT", Kind: view.KindText, InList: true, MobilePrimary: true},
					{Name: "value", Label: "VALUE", Kind: view.KindText, InList: true},
					{Name: "gathered", Label: "GATHERED", Kind: view.KindTimestamp, InList: true},
				}),
			view.Planned("Jobs",
				"What has run against this device, newest first, and what it did.",
				"Per-device outcomes are recorded against the job that produced them and are read by job id; there is no index from a device back to the jobs that touched it.",
				[]view.Field{
					{Name: "job", Label: "JOB", Kind: view.KindText, InList: true, MobilePrimary: true, References: "jobs"},
					{Name: "outcome", Label: "OUTCOME", Kind: view.KindBadge, InList: true},
					{Name: "reason", Label: "REASON", Kind: view.KindText, InList: true},
					{Name: "created", Label: "WHEN", Kind: view.KindText, InList: true},
				}),
			view.Planned("Access",
				"The role bindings that name this device directly.",
				"A device is a grant target in the scope ladder, but bindings key on a numeric device id and this view addresses a device by name.",
				[]view.Field{
					{Name: "team", Label: "TEAM", Kind: view.KindText, InList: true, MobilePrimary: true, References: "teams"},
					{Name: "role", Label: "ROLE", Kind: view.KindText, InList: true},
					{Name: "effect", Label: "EFFECT", Kind: view.KindBadge, InList: true},
				}),
		},
		Fields: fields,
		Ops: view.Ops{
			List:   &apispec.ListDevices,
			Get:    &apispec.GetDevice,
			Create: &apispec.CreateDevice,
			Update: &apispec.UpdateDevice,
			Delete: &apispec.DeleteDevice,
		},
		Applies:  applies,
		Handlers: view.MustBind(reader{repo}, writer{repo}, projector),
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

// Package link: virt.cml.link.create, two nodes' interfaces linked in a
// CML lab. Its registration's Doc is generated from
// internal/forge/catalogdata.
package link

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.cml.link.create",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"https",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("CMLAPICapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteController,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "A run that made a link emits virt.cml.link.delete naming its lab's id and its own; one that found the same link already there emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.cml.link.delete", Record: []string{paramLab, paramLink}, MayKeepOnFailure: true},
				},
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Links two nodes' interfaces in a CML lab.",
				Description: "Links interface a_interface of node a to interface b_interface of node b. The same link already there is unchanged; an interface already linked to anything else is refused. Its undo is virt.cml.link.delete of the link's id. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads both interfaces and changes nothing.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
					{Name: "a", Type: "string", Required: true, Description: "One node, by label or id."},
					{Name: "a_interface", Type: "string", Required: true, Description: "Its interface, by label, e.g. Ethernet0/1."},
					{Name: "b", Type: "string", Required: true, Description: "The other node, by label or id."},
					{Name: "b_interface", Type: "string", Required: true, Description: "Its interface, by label."},
				},
				Returns: []collection.ReturnField{
					{Name: "id", Type: "string", Returned: "always", Description: "The link's id."},
				},
				Examples: []collection.Example{
					{Name: "Link a branch to a core", RunbookYAML: "- name: Link BR1 to CORE1\n  virt.cml.link.create:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: BR1\n    b_interface: Ethernet0/1\n"},
				},
				SeeAlso: []string{"virt.cml.link.delete", "virt.cml.node.create"},
			},
		},
		Invoke: Create,
		Check:  CheckCreate,
	})
}

// Create implements "virt.cml.link.create".
func Create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCreate(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCreate is "virt.cml.link.create"'s check: it reads both interfaces
// and links nothing.
func CheckCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCreate(ctx, rc, device, params, collection.ModeCheck)
}

// runCreate links the two interfaces: the same link already there is
// unchanged, and an interface linked to anything else is refused.
func runCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.link.create"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	ep, err := readEnds(params, true)
	if err != nil {
		return fail(err)
	}
	client, lab, a, b, links, err := resolve(ctx, rc, device, ep)
	if err != nil {
		return fail(cml.Unchecked(mode, err))
	}
	if l, ok := cml.Between(links, a.Interface.ID, b.Interface.ID); ok {
		return collection.Result{}, rc.SetStat("id", l.ID)
	}
	for _, e := range []cml.Endpoint{a, b} {
		if e.Interface.IsConnected {
			return fail(fmt.Errorf("%s %s is already linked to another interface: remove that link first", e.Node.Label, e.Interface.Label))
		}
	}
	if a.Interface.ID == b.Interface.ID {
		return fail(errors.New("a link joins two interfaces, and both ends name the same one"))
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	id, err := client.CreateLink(ctx, lab.ID, a.Interface.ID, b.Interface.ID)
	if err != nil {
		return fail(err)
	}
	// The link exists from here on, so its undo stands even if the task
	// then fails.
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          "virt.cml.link.delete",
		Params:        map[string]any{paramLab: lab.ID, paramLink: id},
		Description:   fmt.Sprintf("Remove the link between %s %s and %s %s this task made.", a.Node.Label, a.Interface.Label, b.Node.Label, b.Interface.Label),
		KeepOnFailure: true,
	}); err != nil {
		return collection.Result{Changed: true}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, rc.SetStat("id", id)
}

// Package link: virt.cml.link.delete, a link removed from a CML lab. Its
// registration's Doc is generated from internal/forge/catalogdata.
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
		Name: "virt.cml.link.delete",
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
				Notes:      "A run that removed a link emits virt.cml.link.create of the same two interfaces, each node by its id and each interface by its label; one that found no such link emits nothing.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.cml.link.create", Record: []string{paramLab, paramA, paramAInterface, paramB, paramBInterface}},
				},
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Removes a link from a CML lab.",
				Description: "Removes the link named by link, or the one between a's a_interface and b's b_interface (one way or the other, never both). A link that does not exist is unchanged. Its undo is virt.cml.link.create of the same two interfaces. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the link exists and changes nothing.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
					{Name: "link", Type: "string", Description: "The link's id."},
					{Name: "a", Type: "string", Description: "One node, by label or id, with a_interface, b and b_interface."},
					{Name: "a_interface", Type: "string", Description: "Its interface, by label."},
					{Name: "b", Type: "string", Description: "The other node, by label or id."},
					{Name: "b_interface", Type: "string", Description: "Its interface, by label."},
				},
				Returns: []collection.ReturnField{
					{Name: "id", Type: "string", Returned: "on change", Description: "The id of the link removed."},
				},
				Examples: []collection.Example{
					{Name: "Cut a branch's uplink", RunbookYAML: "- name: Cut BR1's link to CORE1\n  virt.cml.link.delete:\n    lab: cvd\n    a: CORE1\n    a_interface: Ethernet0/1\n    b: BR1\n    b_interface: Ethernet0/1\n"},
				},
				SeeAlso: []string{"virt.cml.link.create"},
			},
		},
		Invoke: Delete,
		Check:  CheckDelete,
	})
}

// Delete implements "virt.cml.link.delete".
func Delete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDelete is "virt.cml.link.delete"'s check: it reads whether the link
// exists and removes nothing.
func CheckDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeCheck)
}

// runDelete removes the link named by its id or by its two ends. A link
// that does not exist is unchanged.
func runDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.link.delete"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return fail(err)
	}
	byID := sdk.StringParam(params, paramLink)
	ends, err := readEnds(params, byID == "")
	switch {
	case err != nil:
		return fail(err)
	case byID != "" && ends.named():
		return fail(fmt.Errorf("name the link by %s or by its two ends, not both", paramLink))
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return fail(err)
	}
	lab, err := client.FindLab(ctx, ref)
	if errors.Is(err, cml.ErrNotFound) {
		// Gone already, unless an earlier task in the same run makes it,
		// which a check cannot tell.
		if err := cml.Gone(mode, err); err != nil {
			return fail(err)
		}
		return collection.Result{}, nil
	}
	if err != nil {
		return fail(err)
	}
	nodes, err := client.Nodes(ctx, lab.ID)
	if err != nil {
		return fail(err)
	}
	links, err := client.Links(ctx, lab.ID)
	if err != nil {
		return fail(err)
	}
	var link cml.Link
	found := false
	if byID != "" {
		for _, l := range links {
			if l.ID == byID {
				link, found = l, true
			}
		}
	} else {
		a, err := client.FindEndpoint(ctx, lab.ID, nodes, ends.a, ends.aIface)
		if errors.Is(err, cml.ErrNotFound) {
			if err := cml.Gone(mode, err); err != nil {
				return fail(err)
			}
			return collection.Result{}, nil
		}
		if err != nil {
			return fail(err)
		}
		b, err := client.FindEndpoint(ctx, lab.ID, nodes, ends.b, ends.bIface)
		if errors.Is(err, cml.ErrNotFound) {
			if err := cml.Gone(mode, err); err != nil {
				return fail(err)
			}
			return collection.Result{}, nil
		}
		if err != nil {
			return fail(err)
		}
		link, found = cml.Between(links, a.Interface.ID, b.Interface.ID)
	}
	if !found {
		if err := cml.Gone(mode, fmt.Errorf("lab %q has no such link: %w", lab.Title, cml.ErrNotFound)); err != nil {
			return fail(err)
		}
		return collection.Result{}, nil
	}
	undo, described, err := endsOf(ctx, client, lab.ID, nodes, link)
	if err != nil {
		return fail(err)
	}
	if err := rc.SetStat("id", link.ID); err != nil {
		return collection.Result{}, err
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	if err := client.DeleteLink(ctx, lab.ID, link.ID); err != nil {
		return fail(err)
	}
	undo[paramLab] = lab.ID
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "virt.cml.link.create",
		Params:      undo,
		Description: fmt.Sprintf("Link %s again, as they were before this task.", described),
	}); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}

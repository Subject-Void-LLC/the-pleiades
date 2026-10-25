// Package lab: virt.cml.lab.create, an empty lab with a title no other lab
// has. Its registration's Doc is generated from internal/forge/catalogdata.
package lab

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.cml.lab.create",
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
				Notes:      "A run that made a lab emits virt.cml.lab.delete naming its id, so a rollback removes exactly that lab and whatever was later built in it.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.cml.lab.delete", Record: []string{paramLab}, MayKeepOnFailure: true},
				},
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Creates an empty CML lab with a title.",
				Description: "Makes a lab with no nodes. A title a lab already has is refused, since CML lets two labs share one and every later task names its lab by title; a second run of the same task is therefore a refusal, not a second lab. Its undo is virt.cml.lab.delete of the lab's id, so a rollback removes exactly the lab this task made. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the title is free and changes nothing.",
				Params: []collection.Param{
					{Name: "title", Type: "string", Required: true, Description: "The new lab's title."},
				},
				Returns: []collection.ReturnField{
					{Name: "id", Type: "string", Returned: "on change", Description: "The new lab's id."},
					{Name: "title", Type: "string", Returned: "always", Description: "The lab's title."},
				},
				Examples: []collection.Example{
					{Name: "Make a lab", RunbookYAML: "- name: Make the CVD lab\n  virt.cml.lab.create:\n    title: cvd\n"},
				},
				SeeAlso: []string{"virt.cml.node.create", "virt.cml.lab.delete"},
			},
		},
		Invoke: Create,
		Check:  CheckCreate,
	})
}

// Create implements "virt.cml.lab.create".
func Create(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCreate(ctx, rc, device, params, collection.ModeExecute)
}

// CheckCreate is "virt.cml.lab.create"'s check: it reads whether the title
// is free and makes nothing.
func CheckCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCreate(ctx, rc, device, params, collection.ModeCheck)
}

// runCreate makes a lab titled title, refusing a title a lab already has:
// CML lets two labs share one, and every later task names its lab by it.
func runCreate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.lab.create"
	title, err := sdk.RequiredStringParam(params, paramTitle)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	labs, err := client.Labs(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	for _, l := range labs {
		if l.Title == title {
			return collection.Result{}, fmt.Errorf("%s: a lab titled %q already exists (%s): remove it with virt.cml.lab.delete, or choose another title", fqcn, title, l.ID)
		}
	}
	if err := rc.SetStat("title", title); err != nil {
		return collection.Result{}, err
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	lab, err := client.CreateLab(ctx, title)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// The lab exists from here on, so its undo stands even if the task
	// then fails.
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          "virt.cml.lab.delete",
		Params:        map[string]any{paramLab: lab.ID},
		Description:   fmt.Sprintf("Remove the lab %q (%s) this task made.", title, lab.ID),
		KeepOnFailure: true,
	}); err != nil {
		return collection.Result{Changed: true}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, rc.SetStat("id", lab.ID)
}

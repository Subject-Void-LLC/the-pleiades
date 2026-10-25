// Package lab: virt.cml.lab.delete, a lab stopped, wiped and removed, which
// cannot be undone. Its registration's Doc is generated from
// internal/forge/catalogdata.
package lab

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.cml.lab.delete",
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
				Reversible: false,
				Notes:      "Every node's disk and configuration is wiped before the lab is removed, and nothing keeps them, so nothing can put the lab back as it was.",
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Stops, wipes and deletes a CML lab.",
				Description: "Stops every node, wipes every node's disks and removes the lab. A lab that does not exist is unchanged. It cannot be undone: every node's disk and configuration is gone, which is why it is a method of its own and never part of another. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the lab exists and changes nothing.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
				},
				Returns: []collection.ReturnField{
					{Name: "lab_id", Type: "string", Returned: "on change", Description: "The id of the lab removed."},
				},
				Examples: []collection.Example{
					{Name: "Remove a lab", RunbookYAML: "- name: Remove the CVD lab\n  virt.cml.lab.delete:\n    lab: cvd\n"},
				},
				SeeAlso: []string{"virt.cml.lab.create"},
			},
		},
		Invoke: Delete,
		Check:  CheckDelete,
	})
}

// stopWait bounds how long a delete waits for the lab's nodes to stop
// before it wipes them.
const stopWait = 5 * time.Minute

// Delete implements "virt.cml.lab.delete".
func Delete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDelete is "virt.cml.lab.delete"'s check: it reads whether the lab
// exists and removes nothing.
func CheckDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeCheck)
}

// runDelete stops every node of the lab, waits until none runs, wipes the
// lab and removes it. A lab that does not exist is unchanged.
func runDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.lab.delete"
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	lab, err := client.FindLab(ctx, ref)
	if missing(err) {
		// Gone already, unless an earlier task in the same run makes it,
		// which a check cannot tell.
		if err := cml.Gone(mode, err); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{}, nil
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat("lab_id", lab.ID); err != nil {
		return collection.Result{}, err
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	if err := client.StopLab(ctx, lab.ID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	wait, cancel := context.WithTimeout(ctx, stopWait)
	defer cancel()
	if err := client.WaitStopped(wait, lab.ID, nil, pollInterval); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := client.WipeLab(ctx, lab.ID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := client.DeleteLab(ctx, lab.ID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

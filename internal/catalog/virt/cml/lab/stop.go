// Package lab: virt.cml.lab.stop, a lab's nodes stopped with their disks
// kept. Its registration's Doc is generated from internal/forge/catalogdata.
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
		Name: "virt.cml.lab.stop",
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
				Notes:      "A run that stopped nodes emits virt.cml.lab.start naming exactly those nodes, kept when a stop fails partway, so a rollback starts only what this task stopped.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.cml.lab.start", Record: []string{paramLab, paramNodes}, MayKeepOnFailure: true},
				},
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Stops a CML lab's nodes.",
				Description: "Stops each of the lab's nodes (or those nodes names) that is running, keeping their disks. A node already stopped is left alone. Its undo is virt.cml.lab.start of exactly the nodes this task stopped. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads which nodes it would stop and changes nothing.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
					{Name: "nodes", Type: "list", Description: "The nodes to act on, by label or id; every node of the lab when empty. A rollback sets it to exactly the nodes the run changed."},
				},
				Returns: []collection.ReturnField{
					{Name: "stopped", Type: "list", Returned: "always", Description: "The labels of the nodes this task stopped."},
					{Name: "lab_id", Type: "string", Returned: "always", Description: "The lab's id."},
				},
				Examples: []collection.Example{
					{Name: "Stop one node", RunbookYAML: "- name: Take CORE1 down to prove the second hub carries the branches\n  virt.cml.lab.stop:\n    lab: cvd\n    nodes:\n      - CORE1\n"},
				},
				SeeAlso: []string{"virt.cml.lab.start"},
			},
		},
		Invoke: Stop,
		Check:  CheckStop,
	})
}

// Stop implements "virt.cml.lab.stop".
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStop is "virt.cml.lab.stop"'s check: it reads which nodes a run
// would stop and stops none.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStop(ctx, rc, device, params, collection.ModeCheck)
}

// runStop stops each chosen node that is running and records the start of
// exactly those.
func runStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.lab.stop"
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	refs, _, err := sdk.StringSlice(params, paramNodes)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	lab, err := findLab(ctx, client, ref, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	nodes, err := client.Nodes(ctx, lab.ID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	chosen, err := pick(nodes, refs)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	var todo []cml.Node
	for _, n := range chosen {
		if cml.Running(n.State) {
			todo = append(todo, n)
		}
	}
	labels, _ := labelsAndIDs(todo)
	if err := rc.SetStat("lab_id", lab.ID); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat("stopped", labels); err != nil {
		return collection.Result{}, err
	}
	if len(todo) == 0 || mode == collection.ModeCheck {
		return collection.Result{Changed: len(todo) > 0}, nil
	}
	var stopped []cml.Node
	var stopErr error
	for _, n := range todo {
		if stopErr = client.StopNode(ctx, lab.ID, n.ID); stopErr != nil {
			break
		}
		stopped = append(stopped, n)
	}
	if len(stopped) > 0 {
		_, ids := labelsAndIDs(stopped)
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:          "virt.cml.lab.start",
			Params:        map[string]any{paramLab: lab.ID, paramNodes: ids},
			Description:   fmt.Sprintf("Start the %d node(s) of %q this task stopped, and no other.", len(ids), lab.Title),
			KeepOnFailure: true,
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if stopErr != nil {
		return collection.Result{Changed: len(stopped) > 0}, fmt.Errorf("%s: %w", fqcn, stopErr)
	}
	return collection.Result{Changed: true}, nil
}

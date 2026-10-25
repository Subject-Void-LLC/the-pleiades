// Package node: virt.cml.node.delete, a node stopped, wiped and removed,
// which cannot be undone. Its registration's Doc is generated from
// internal/forge/catalogdata.
package node

import (
	"context"
	"errors"
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
		Name: "virt.cml.node.delete",
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
				Notes:      "The node's disk and configuration are wiped before it is removed, and nothing keeps them, so nothing can put it back as it was.",
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Stops, wipes and deletes one node of a CML lab.",
				Description: "Stops the node, wipes its disks and removes it with its links. A node that does not exist is unchanged. It cannot be undone: the node's disk and configuration are gone. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the node exists and changes nothing.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
					{Name: "node", Type: "string", Required: true, Description: "The node, by label or id."},
				},
				Returns: []collection.ReturnField{
					{Name: "node_id", Type: "string", Returned: "on change", Description: "The id of the node removed."},
				},
				Examples: []collection.Example{
					{Name: "Remove a node", RunbookYAML: "- name: Remove BR3\n  virt.cml.node.delete:\n    lab: cvd\n    node: BR3\n"},
				},
				SeeAlso: []string{"virt.cml.node.create"},
			},
		},
		Invoke: Delete,
		Check:  CheckDelete,
	})
}

// pollInterval is how often a delete reads whether the node has stopped.
var pollInterval = 5 * time.Second

// stopWait bounds how long a delete waits for the node to stop.
const stopWait = 5 * time.Minute

// Delete implements "virt.cml.node.delete".
func Delete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDelete is "virt.cml.node.delete"'s check: it reads whether the node
// exists and removes nothing.
func CheckDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runDelete(ctx, rc, device, params, collection.ModeCheck)
}

// runDelete stops the node, waits until it has, wipes it and removes it. A
// lab or node that does not exist is unchanged.
func runDelete(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.node.delete"
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return fail(err)
	}
	nodeRef, err := sdk.RequiredStringParam(params, paramNode)
	if err != nil {
		return fail(err)
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
	n, err := cml.FindNode(nodes, nodeRef)
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
	if err := rc.SetStat("node_id", n.ID); err != nil {
		return collection.Result{}, err
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	if cml.Running(n.State) {
		if err := client.StopNode(ctx, lab.ID, n.ID); err != nil {
			return fail(err)
		}
		wait, cancel := context.WithTimeout(ctx, stopWait)
		defer cancel()
		if err := client.WaitStopped(wait, lab.ID, []string{n.ID}, pollInterval); err != nil {
			return fail(err)
		}
	}
	if err := client.WipeNode(ctx, lab.ID, n.ID); err != nil {
		return fail(err)
	}
	if err := client.DeleteNode(ctx, lab.ID, n.ID); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}

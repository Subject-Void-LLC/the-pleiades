// Package lab: virt.cml.lab.start, a lab's nodes started and waited for
// until they boot. Its registration's Doc is generated from
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
		Name: "virt.cml.lab.start",
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
				Notes:      "A run that started nodes emits virt.cml.lab.stop naming exactly those nodes, kept when the wait for them to boot fails, so a rollback never stops a node someone else started. Stopping them does not undo what they did while they ran.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "virt.cml.lab.stop", Record: []string{paramLab, paramNodes}, MayKeepOnFailure: true},
				},
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Starts a CML lab's nodes and waits until they boot.",
				Description: "Starts each of the lab's nodes (or those nodes names) that is not running, then waits until every one of them reports BOOTED, up to timeout. A node already running is left alone, and a lab whose nodes all run is unchanged. Its undo is virt.cml.lab.stop of exactly the nodes this task started, so a rollback never stops a node someone else started. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads which nodes it would start and changes nothing.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
					{Name: "nodes", Type: "list", Description: "The nodes to act on, by label or id; every node of the lab when empty. A rollback sets it to exactly the nodes the run changed."},
					{Name: "timeout", Type: "int", Default: "900", Description: "Seconds to wait for every node started to report BOOTED."},
				},
				Returns: []collection.ReturnField{
					{Name: "started", Type: "list", Returned: "always", Description: "The labels of the nodes this task started."},
					{Name: "lab_id", Type: "string", Returned: "always", Description: "The lab's id."},
				},
				Examples: []collection.Example{
					{Name: "Start a lab", RunbookYAML: "- name: Start the CVD lab\n  virt.cml.lab.start:\n    lab: cvd\n    timeout: 1200\n"},
				},
				SeeAlso: []string{"virt.cml.lab.stop"},
			},
		},
		Invoke: Start,
		Check:  CheckStart,
	})
}

// pollInterval is how often a start or a delete reads the nodes' states.
var pollInterval = 5 * time.Second

// Start implements "virt.cml.lab.start".
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeExecute)
}

// CheckStart is "virt.cml.lab.start"'s check: it reads which nodes a run
// would start and starts none.
func CheckStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runStart(ctx, rc, device, params, collection.ModeCheck)
}

// runStart starts each chosen node that is not running, records the stop
// of exactly those, then waits until each reports BOOTED.
func runStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "virt.cml.lab.start"
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	refs, _, err := sdk.StringSlice(params, paramNodes)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	timeout, set, err := sdk.IntParam(params, paramTimeout)
	switch {
	case err != nil:
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	case !set:
		timeout = 900
	case timeout <= 0:
		return collection.Result{}, fmt.Errorf("%s: %s must be a number of seconds above zero", fqcn, paramTimeout)
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	lab, err := findLab(ctx, client, ref, fqcn)
	if err != nil {
		return collection.Result{}, cml.Unchecked(mode, err)
	}
	nodes, err := client.Nodes(ctx, lab.ID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	chosen, err := pick(nodes, refs)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, cml.Unchecked(mode, err))
	}
	var todo []cml.Node
	for _, n := range chosen {
		if !cml.Running(n.State) {
			todo = append(todo, n)
		}
	}
	labels, _ := labelsAndIDs(todo)
	undoScope, err := undoNodes(nodes, todo, "start")
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat("lab_id", lab.ID); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat("started", labels); err != nil {
		return collection.Result{}, err
	}
	if len(todo) == 0 || mode == collection.ModeCheck {
		return collection.Result{Changed: len(todo) > 0}, nil
	}
	var started []cml.Node
	var startErr error
	for _, n := range todo {
		if startErr = client.StartNode(ctx, lab.ID, n.ID); startErr != nil {
			break
		}
		started = append(started, n)
	}
	if len(started) > 0 {
		_, ids := labelsAndIDs(started)
		undo := map[string]any{paramLab: lab.ID}
		if len(undoScope) > 0 {
			undo[paramNodes] = ids
		}
		if err := sdk.RecordInverse(rc, sdk.Inverse{
			FQCN:          "virt.cml.lab.stop",
			Params:        undo,
			Description:   fmt.Sprintf("Stop the %d node(s) of %q this task started, and no other.", len(ids), lab.Title),
			KeepOnFailure: true,
		}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	if startErr != nil {
		return collection.Result{Changed: len(started) > 0}, fmt.Errorf("%s: %w", fqcn, startErr)
	}
	_, ids := labelsAndIDs(started)
	wait, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	if err := client.WaitBooted(wait, lab.ID, ids, pollInterval); err != nil {
		return collection.Result{Changed: true}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, nil
}

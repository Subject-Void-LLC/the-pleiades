package systemd

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotesvc"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.systemd.start",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSystemd},
			ExecutionContext: collection.ExecutionContext{
				// Talking to the system manager to start a unit needs
				// root on any ordinary configuration.
				RequiresElevation: true,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that started a stopped unit emits an svc.systemd.stop naming it. A run that found it " +
					"already running emits nothing, since undoing it means doing nothing. What the inverse cannot " +
					"restore is anything the service did while it was up: connections it accepted, files it wrote, " +
					"messages it consumed. Stopping it again returns the unit to where it was, not the system.",
			},
			// A check reads the unit's state and says whether a start would
			// be sent, without sending it. See CheckStart.
			SupportsCheck: true,
			Doc:           startDoc(),
		},
		Invoke: Start,
		Check:  CheckStart,
	})
}

func startDoc() collection.Doc {
	return unitDoc(
		"Starts a systemd unit now, without changing whether it starts at boot.",
		"Makes sure a systemd unit is running right now. This is ansible.builtin.systemd with state=started, and it is deliberately not also enable: starting and enabling are separate in systemd and separate here, so a task that wants both says both. State is read before anything is sent, so a unit that is already running reports no change and no command reaches the device. A unit systemd does not know is refused rather than reported as started, and a masked unit is refused with the mask named, since systemd's own error for that case describes a symlink rather than the cause.",
		nil,
		[]collection.Example{
			{
				Name:        "Start a service",
				RunbookYAML: "- name: Make sure nginx is running\n  svc.systemd.start:\n    name: nginx\n",
			},
			{
				Name:        "Start it and make it survive a reboot",
				RunbookYAML: "- name: Start nginx\n  svc.systemd.start:\n    name: nginx\n\n- name: Make nginx start at boot too\n  svc.systemd.enable:\n    name: nginx\n",
			},
		},
		[]string{"svc.systemd.stop", "svc.systemd.restart", "svc.systemd.enable", "svc.start"},
	)
}

// startOp is what makes svc.systemd.start different from its siblings.
// Start and CheckStart both run it, so the two cannot disagree about
// when a unit counts as started.
var startOp = unitOp{
	fqcn:          "svc.systemd.start",
	converged:     remotesvc.State.Active,
	apply:         remotesvc.Start,
	predict:       predictActiveState(activeStateActive),
	refusesMasked: true,
	inverse: func(unit string, _ remotesvc.State) (sdk.Inverse, bool) {
		// Reached only when the run actually started the unit, so the
		// state it was found in was "not running" and stopping it is the
		// exact reverse.
		return sdk.Inverse{
			FQCN:   "svc.systemd.stop",
			Params: map[string]any{paramName: unit},
			Description: fmt.Sprintf("Stop %s, which this task started. It does not undo anything the service did "+
				"while it was running.", unit),
		}, true
	},
}

// Start implements "svc.systemd.start".
//
// A unit that is already active is left alone and the task reports no
// change. "activating" deliberately does not count as active: a unit
// part-way through starting has not started, and treating it as done
// would let this report success for a unit that goes on to fail.
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, startOp, collection.ModeExecute)
}

// CheckStart is "svc.systemd.start" in check mode: it reads the unit and
// reports whether Start would send a start, sending nothing.
//
// It predicts a change for exactly the units Start would act on, since
// both go through runUnitOp with the same startOp, and its diff's after
// half is the unit with ActiveState "active". It refuses an unknown or
// masked unit the same way Start does, so a dry run of a typo fails
// rather than predicting a start systemd would never perform.
func CheckStart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, startOp, collection.ModeCheck)
}

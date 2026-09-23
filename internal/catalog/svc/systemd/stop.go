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
		Name: "svc.systemd.stop",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSystemd},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that stopped a running unit emits an svc.systemd.start naming it. A run that found it " +
					"already stopped emits nothing. Starting it again returns the unit to running, but nothing can " +
					"restore what the service missed while it was down: requests that were refused, queues that " +
					"backed up, timers that did not fire.",
			},
			// A check reads the unit's state and says whether a stop would
			// be sent, without sending it. See CheckStop.
			SupportsCheck: true,
			Doc:           stopDoc(),
		},
		Invoke: Stop,
		Check:  CheckStop,
	})
}

func stopDoc() collection.Doc {
	return unitDoc(
		"Stops a systemd unit now, without changing whether it starts at boot.",
		"Makes sure a systemd unit is not running right now. This is ansible.builtin.systemd with state=stopped, and it leaves the boot-time setting alone: a unit stopped by this task still starts at the next reboot unless svc.systemd.disable is also run. State is read before anything is sent, so a unit that is already stopped reports no change. A unit systemd does not know is refused rather than reported as stopped, which matters more here than anywhere else in this namespace: systemctl answers \"inactive\" for a name that has never existed, so a method trusting it would report success for a typo.",
		nil,
		[]collection.Example{
			{
				Name:        "Stop a service",
				RunbookYAML: "- name: Stop nginx before swapping its config\n  svc.systemd.stop:\n    name: nginx\n",
			},
			{
				Name:        "Stop it now and keep it from coming back at boot",
				RunbookYAML: "- name: Stop nginx\n  svc.systemd.stop:\n    name: nginx\n\n- name: Keep nginx from starting at boot\n  svc.systemd.disable:\n    name: nginx\n",
			},
		},
		[]string{"svc.systemd.start", "svc.systemd.disable", "svc.stop"},
	)
}

// stopOp is what makes svc.systemd.stop different from its siblings,
// shared by Stop and CheckStop.
var stopOp = unitOp{
	fqcn:      "svc.systemd.stop",
	converged: func(s remotesvc.State) bool { return !s.Active() },
	apply:     remotesvc.Stop,
	predict:   predictActiveState(activeStateInactive),
	inverse: func(unit string, _ remotesvc.State) (sdk.Inverse, bool) {
		return sdk.Inverse{
			FQCN:   "svc.systemd.start",
			Params: map[string]any{paramName: unit},
			Description: fmt.Sprintf("Start %s, which this task stopped. It does not recover anything the service "+
				"missed while it was down.", unit),
		}, true
	},
}

// Stop implements "svc.systemd.stop".
//
// A masked unit is NOT refused here, unlike start: a masked unit cannot
// be running, so asking to stop it is already satisfied and refusing
// would fail a task whose goal is met.
func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, stopOp, collection.ModeExecute)
}

// CheckStop is "svc.systemd.stop" in check mode: it reads the unit and
// reports whether Stop would send a stop, sending nothing.
//
// A running unit is predicted to end up with ActiveState "inactive". A
// unit that is not running, including a masked one, is converged, so the
// check predicts no change for it exactly as Stop would make none.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, stopOp, collection.ModeCheck)
}

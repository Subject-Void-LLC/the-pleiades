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
		Name: "svc.systemd.disable",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSystemd},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that disabled an enabled unit emits an svc.systemd.enable naming it. A run that found it " +
					"already disabled emits nothing. One thing the undo does not preserve: a unit that was " +
					"enabled-runtime, meaning enabled only until the next reboot, comes back as permanently enabled, " +
					"because systemctl enable has no way to say \"only until reboot\" about a unit it is restoring.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "svc.systemd.enable", Record: []string{"name"}},
				},
			},
			// A check reads the unit's state and says whether a disable
			// would be sent, without sending it. See CheckDisable.
			SupportsCheck: true,
			Doc:           disableDoc(),
		},
		Invoke: Disable,
		Check:  CheckDisable,
	})
}

func disableDoc() collection.Doc {
	return unitDoc(
		"Stops a systemd unit starting at boot, without stopping it now.",
		"Makes sure a systemd unit is not set to start at boot. This is ansible.builtin.systemd with enabled=no, and it leaves the running system alone: a unit disabled by this task keeps running until svc.systemd.stop runs or the device reboots. State is read before anything is sent, so a unit that is already disabled reports no change. A static unit is refused, since a unit with no [Install] section was never enabled and cannot be disabled.",
		nil,
		[]collection.Example{
			{
				Name:        "Keep a service from starting at boot",
				RunbookYAML: "- name: Stop nginx coming back after a reboot\n  svc.systemd.disable:\n    name: nginx\n",
			},
		},
		[]string{"svc.systemd.enable", "svc.systemd.stop", "svc.disable"},
	)
}

// Disable implements "svc.systemd.disable".
//
// A masked unit is not refused: masking already prevents it starting at
// boot, so the task's goal is met and the run converges rather than
// failing.
func Disable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, disableOp, collection.ModeExecute)
}

// disableOp is what makes svc.systemd.disable different from its
// siblings, shared by Disable and CheckDisable.
var disableOp = unitOp{
	fqcn:                "svc.systemd.disable",
	converged:           func(s remotesvc.State) bool { return !s.Enabled() },
	apply:               remotesvc.Disable,
	predict:             predictUnitFileState(unitFileDisabled),
	needsInstallSection: true,
	inverse: func(unit string, before remotesvc.State) (sdk.Inverse, bool) {
		// The description says which of the two enabled states was found,
		// because svc.systemd.enable can only produce the permanent one and
		// an operator restoring an enabled-runtime unit should know the
		// difference is not being preserved.
		note := ""
		if before.UnitFileState == "enabled-runtime" {
			note = " It was enabled only until the next reboot, and this brings it back permanently enabled, which systemctl enable cannot express otherwise."
		}
		return sdk.Inverse{
			FQCN:        "svc.systemd.enable",
			Params:      map[string]any{paramName: unit},
			Description: fmt.Sprintf("Make %s start at boot again, which is what this task changed.%s", unit, note),
		}, true
	},
}

// CheckDisable is "svc.systemd.disable" in check mode: it reads the unit
// and reports whether Disable would send a disable, sending nothing.
//
// An enabled unit, enabled-runtime included, is predicted to end up with
// UnitFileState "disabled" and its running state untouched. That is the
// same belief Disable acts on. One case is worth knowing about: a plain
// `systemctl disable` works on the persistent configuration under /etc,
// and an enabled-runtime unit's link lives under /run, so on real systemd
// such a unit may well stay enabled-runtime. If it does, Disable's own
// read-back records that, and this prediction was wrong in the same way
// Disable's "changed" was. That is reasoned from systemd's documentation,
// not yet measured against a real systemd.
func CheckDisable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, disableOp, collection.ModeCheck)
}

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
		Name: "svc.systemd.enable",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSystemd},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that enabled a disabled unit emits an svc.systemd.disable naming it. A run that found it " +
					"already enabled emits nothing. The undo restores the boot-time setting only; it never stops a " +
					"unit that is running, because enabling never started one.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "svc.systemd.disable", Record: []string{"name"}},
				},
			},
			// A check reads the unit's state and says whether an enable
			// would be sent, without sending it. See CheckEnable.
			SupportsCheck: true,
			Doc:           enableDoc(),
		},
		Invoke: Enable,
		Check:  CheckEnable,
	})
}

func enableDoc() collection.Doc {
	return unitDoc(
		"Makes a systemd unit start at boot, without starting it now.",
		"Makes sure a systemd unit is set to start at boot. This is ansible.builtin.systemd with enabled=yes, and it is deliberately not also start: a unit enabled by this task is not running until svc.systemd.start runs or the device reboots. State is read before anything is sent, so a unit that is already enabled reports no change. A static unit is refused with that word named, because systemd's own failure for enabling one describes a missing symlink rather than the cause, which is that the unit has no [Install] section and is meant to be pulled in by another unit.",
		nil,
		[]collection.Example{
			{
				Name:        "Make a service start at boot",
				RunbookYAML: "- name: Make sure nginx comes back after a reboot\n  svc.systemd.enable:\n    name: nginx\n",
			},
		},
		[]string{"svc.systemd.disable", "svc.systemd.start", "svc.enable"},
	)
}

// Enable implements "svc.systemd.enable".
//
// "enabled-runtime" counts as enabled, so a unit enabled only until the
// next reboot is left alone rather than being re-enabled on every run.
func Enable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, enableOp, collection.ModeExecute)
}

// enableOp is what makes svc.systemd.enable different from its siblings,
// shared by Enable and CheckEnable.
var enableOp = unitOp{
	fqcn:                "svc.systemd.enable",
	converged:           remotesvc.State.Enabled,
	apply:               remotesvc.Enable,
	predict:             predictUnitFileState(unitFileEnabled),
	needsInstallSection: true,
	refusesMasked:       true,
	inverse: func(unit string, _ remotesvc.State) (sdk.Inverse, bool) {
		return sdk.Inverse{
			FQCN:   "svc.systemd.disable",
			Params: map[string]any{paramName: unit},
			Description: fmt.Sprintf("Stop %s starting at boot, which is what this task changed. It leaves the "+
				"unit running if it is running now, since enabling it never started it.", unit),
		}, true
	},
}

// CheckEnable is "svc.systemd.enable" in check mode: it reads the unit and
// reports whether Enable would send an enable, sending nothing.
//
// A disabled unit is predicted to end up with UnitFileState "enabled" and
// its running state untouched. A static or masked unit is refused the
// same way Enable refuses it, so a dry run never promises a boot-time
// change systemctl would reject.
func CheckEnable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, enableOp, collection.ModeCheck)
}

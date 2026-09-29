package windows

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmsvc"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.windows.enable",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"winrm"},
			RequiredCapabilities: []capability.Name{capability.NameWindowsService},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: true,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// It reads the service before it acts, so a check can predict
			// through the same code (CheckEnable).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that enabled a service whose start type was Disabled emits an svc.windows.disable naming " +
					"it. A run that found the start type already Automatic emits nothing. A run that changed a start " +
					"type of Manual to Automatic emits no inverse at all, deliberately: this namespace's disable only " +
					"sets Disabled, which is a stronger claim than restoring Manual, and emitting an instruction that " +
					"would over-correct is worse than emitting none. The undo restores the boot-time setting only; it " +
					"never stops a service that is running, because enabling never started one.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "svc.windows.disable", Record: []string{"name"}},
				},
			},
			Doc: enableDoc(),
		},
		Invoke: Enable,
		Check:  CheckEnable,
	})
}

func enableDoc() collection.Doc {
	return serviceDoc(
		"Makes a Windows service start at boot, without starting it now.",
		"Makes sure a Windows service's start type is Automatic. This is ansible.windows.win_service with start_mode=auto, and it is deliberately not also start: a service enabled by this task is not running until svc.windows.start runs or the device reboots. State is read before anything is sent, so a service whose start type is already Automatic reports no change. Windows recognizes a third start type, Manual, that neither this method nor svc.windows.disable targets: a service found Manual becomes Automatic, and running svc.windows.disable afterward would leave it Disabled rather than back at Manual, which is why that specific transition records no inverse instruction rather than a wrong one.",
		nil,
		[]collection.Example{
			{
				Name:        "Make a service start at boot",
				RunbookYAML: "- name: Make sure the print spooler comes back after a reboot\n  svc.windows.enable:\n    name: Spooler\n",
			},
		},
		[]string{"svc.windows.disable", "svc.windows.start", "svc.enable"},
	)
}

// Enable implements "svc.windows.enable".
// enableOp is svc.windows.enable's operation, shared by Enable and CheckEnable so the
// method and its check cannot disagree about it. A function rather than a
// variable, so it reads the service functions (startFunc and the rest) when
// it is called, which is what lets a test replace them.
func enableOp() serviceOp {
	return serviceOp{
		fqcn:      "svc.windows.enable",
		converged: func(s winrmsvc.State) bool { return s.StartType == "Automatic" },
		apply:     enableFunc,
		inverse: func(name string, before winrmsvc.State) (sdk.Inverse, bool) {
			// Only a Disabled->Automatic transition has an exact reverse
			// through this namespace's own methods: svc.windows.disable
			// sets Disabled, which is the state this run actually found.
			// A Manual->Automatic transition has no exact reverse here --
			// see the manifest's Reversibility.Notes for why disable would
			// over-correct it -- so this reports false and emits nothing.
			if before.StartType != "Disabled" {
				return sdk.Inverse{}, false
			}
			return sdk.Inverse{
				FQCN:   "svc.windows.disable",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Stop %s starting at boot, which is what this task changed. It leaves the "+
					"service running if it is running now, since enabling it never started it.", name),
			}, true
		},
		predict: func(s winrmsvc.State) winrmsvc.State {
			s.StartType = "Automatic"
			return s
		},
	}
}

func Enable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, enableOp(), collection.ModeExecute)
}

// CheckEnable is svc.windows.enable's check: the same Get-Service read, refusals
// and decision as Enable, then a prediction (StartType Automatic) instead of the change.
func CheckEnable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, enableOp(), collection.ModeCheck)
}

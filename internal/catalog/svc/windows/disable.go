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
		Name: "svc.windows.disable",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"winrm"},
			RequiredCapabilities: []capability.Name{capability.NameWindowsService},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: true,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that disabled a service whose start type was Automatic emits an svc.windows.enable " +
					"naming it. A run that found the start type already Disabled emits nothing. A run that changed a " +
					"start type of Manual to Disabled emits no inverse at all, deliberately: this namespace's enable " +
					"only sets Automatic, which is not what Manual was, and emitting an instruction that would " +
					"over-correct is worse than emitting none. The undo restores the boot-time setting only; it never " +
					"starts a service, because disabling never stopped one running right now.",
			},
			Doc: disableDoc(),
		},
		Invoke: Disable,
	})
}

func disableDoc() collection.Doc {
	return serviceDoc(
		"Stops a Windows service starting at boot, without stopping it now.",
		"Makes sure a Windows service's start type is Disabled, refusing it from starting at all until this is undone. This is ansible.windows.win_service with start_mode=disabled, and it leaves the running system alone: a service disabled by this task keeps running until svc.windows.stop also runs or it is stopped some other way. State is read before anything is sent, so a service whose start type is already Disabled reports no change. Windows recognizes a third start type, Manual, that neither this method nor svc.windows.enable targets: a service found Manual becomes Disabled, and running svc.windows.enable afterward would leave it Automatic rather than back at Manual, which is why that specific transition records no inverse instruction rather than a wrong one.",
		nil,
		[]collection.Example{
			{
				Name:        "Keep a service from starting at boot",
				RunbookYAML: "- name: Make sure the print spooler cannot start at boot\n  svc.windows.disable:\n    name: Spooler\n",
			},
			{
				Name:        "Disable it and stop it running now too",
				RunbookYAML: "- name: Stop the print spooler\n  svc.windows.stop:\n    name: Spooler\n\n- name: Keep the print spooler from starting at boot\n  svc.windows.disable:\n    name: Spooler\n",
			},
		},
		[]string{"svc.windows.enable", "svc.windows.stop", "svc.disable"},
	)
}

// Disable implements "svc.windows.disable".
func Disable(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, serviceOp{
		fqcn:      "svc.windows.disable",
		converged: winrmsvc.State.Disabled,
		apply:     disableFunc,
		inverse: func(name string, before winrmsvc.State) (sdk.Inverse, bool) {
			// Symmetric to Enable's own inverse: only an Automatic->Disabled
			// transition has an exact reverse through svc.windows.enable,
			// which sets Automatic. A Manual->Disabled transition has no
			// exact reverse here; see the manifest's Reversibility.Notes.
			if before.StartType != "Automatic" {
				return sdk.Inverse{}, false
			}
			return sdk.Inverse{
				FQCN:   "svc.windows.enable",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Make %s start at boot again, which is what this task changed. It does not "+
					"start the service now, since disabling it never stopped a running one.", name),
			}, true
		},
	})
}

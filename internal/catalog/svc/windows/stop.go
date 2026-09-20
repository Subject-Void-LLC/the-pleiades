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
		Name: "svc.windows.stop",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"winrm"},
			RequiredCapabilities: []capability.Name{capability.NameWindowsService},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: true,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusImplemented,
			// It reads the service before it acts, so a check can predict
			// through the same code (CheckStop).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that stopped a running service emits an svc.windows.start naming it. A run that found it " +
					"already stopped emits nothing. Starting it again returns the service to running, but nothing can " +
					"restore what it missed while it was down: requests that were refused, queues that backed up, " +
					"timers that did not fire.",
			},
			Doc: stopDoc(),
		},
		Invoke: Stop,
		Check:  CheckStop,
	})
}

func stopDoc() collection.Doc {
	return serviceDoc(
		"Stops a Windows service now, without changing its start type.",
		"Makes sure a Windows service is not running right now. This is ansible.windows.win_service with state=stopped, and it leaves the start type alone: a service stopped by this task still starts at the next reboot unless svc.windows.disable is also run. State is read before anything is sent, so a service that is already stopped reports no change. A service the Service Control Manager does not know is refused rather than reported as stopped, since a typo in the name should not read as success.",
		nil,
		[]collection.Example{
			{
				Name:        "Stop a service",
				RunbookYAML: "- name: Stop the print spooler before changing its config\n  svc.windows.stop:\n    name: Spooler\n",
			},
			{
				Name:        "Stop it now and keep it from coming back at boot",
				RunbookYAML: "- name: Stop the print spooler\n  svc.windows.stop:\n    name: Spooler\n\n- name: Keep the print spooler from starting at boot\n  svc.windows.disable:\n    name: Spooler\n",
			},
		},
		[]string{"svc.windows.start", "svc.windows.disable", "svc.stop"},
	)
}

// Stop implements "svc.windows.stop".
//
// A Disabled service is NOT refused here, unlike start: a Disabled
// service cannot be running, so asking to stop it is already satisfied
// and refusing would fail a task whose goal is met.
// stopOp is svc.windows.stop's operation, shared by Stop and CheckStop so the
// method and its check cannot disagree about it. A function rather than a
// variable, so it reads the service functions (startFunc and the rest) when
// it is called, which is what lets a test replace them.
func stopOp() serviceOp {
	return serviceOp{
		fqcn:      "svc.windows.stop",
		converged: func(s winrmsvc.State) bool { return !s.Running() },
		apply:     stopFunc,
		inverse: func(name string, _ winrmsvc.State) (sdk.Inverse, bool) {
			return sdk.Inverse{
				FQCN:   "svc.windows.start",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Start %s, which this task stopped. It does not recover anything the service "+
					"missed while it was down.", name),
			}, true
		},
		predict: func(s winrmsvc.State) winrmsvc.State {
			s.Status = "Stopped"
			return s
		},
	}
}

func Stop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, stopOp(), collection.ModeExecute)
}

// CheckStop is svc.windows.stop's check: the same Get-Service read, refusals
// and decision as Stop, then a prediction (Status Stopped) instead of the change.
func CheckStop(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, stopOp(), collection.ModeCheck)
}

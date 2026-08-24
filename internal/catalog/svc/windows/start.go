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
		Name: "svc.windows.start",
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
				Notes: "A run that started a stopped service emits an svc.windows.stop naming it. A run that found it " +
					"already running emits nothing. What the inverse cannot restore is anything the service did while " +
					"it was up: connections it accepted, files it wrote, messages it consumed. Stopping it again " +
					"returns the service to where it was, not the system.",
			},
			Doc: startDoc(),
		},
		Invoke: Start,
	})
}

func startDoc() collection.Doc {
	return serviceDoc(
		"Starts a Windows service now, without changing its start type.",
		"Makes sure a Windows service is running right now. This is ansible.windows.win_service with state=started, and it is deliberately not also enable: starting and setting the start type are separate in the Service Control Manager and separate here, so a task that wants both says both. State is read before anything is sent, so a service that is already running reports no change and no command reaches the device. A service the Service Control Manager does not know is refused rather than reported as started, and a service whose start type is Disabled is refused with that named, since Windows itself refuses to start one and its own error describes a generic failure rather than the cause.",
		nil,
		[]collection.Example{
			{
				Name:        "Start a service",
				RunbookYAML: "- name: Make sure the print spooler is running\n  svc.windows.start:\n    name: Spooler\n",
			},
			{
				Name:        "Start it and make it survive a reboot",
				RunbookYAML: "- name: Start the print spooler\n  svc.windows.start:\n    name: Spooler\n\n- name: Make the print spooler start at boot too\n  svc.windows.enable:\n    name: Spooler\n",
			},
		},
		[]string{"svc.windows.stop", "svc.windows.restart", "svc.windows.enable", "svc.start"},
	)
}

// Start implements "svc.windows.start".
//
// A service that is already running is left alone and the task reports
// no change. "StartPending" deliberately does not count as running: a
// service part-way through starting has not started, and treating it as
// done would let this report success for one that goes on to fail.
func Start(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, serviceOp{
		fqcn:            "svc.windows.start",
		converged:       winrmsvc.State.Running,
		apply:           startFunc,
		refusesDisabled: true,
		inverse: func(name string, _ winrmsvc.State) (sdk.Inverse, bool) {
			// Reached only when the run actually started the service, so
			// the state it was found in was "not running" and stopping it
			// is the exact reverse.
			return sdk.Inverse{
				FQCN:   "svc.windows.stop",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Stop %s, which this task started. It does not undo anything the service did "+
					"while it was running.", name),
			}, true
		},
	})
}

package windows

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmsvc"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.windows.restart",
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
			// through the same code (CheckRestart).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "A restart's effect is the interruption itself, and there is no instruction that un-interrupts " +
					"a service. Restarting it a second time would run this method again rather than reverse it. The " +
					"service's state is still recorded under diff, so a rollback reaching this task can see the " +
					"service was running before and after and decide for itself, but this method emits no instruction " +
					"because none would be true.",
			},
			Doc: restartDoc(),
		},
		Invoke: Restart,
		Check:  CheckRestart,
	})
}

func restartDoc() collection.Doc {
	return serviceDoc(
		"Restarts a Windows service, starting it if it was not running.",
		"Restarts a Windows service. This is ansible.windows.win_service with state=restarted, and like that module it starts a service that was not running rather than failing. It is the one method in this namespace that is never converged: restarting a running service is the point, not a no-op, so this always sends the command and always reports changed. That makes it the method most worth putting behind a when condition or a handler, so a service is only bounced when something it reads actually changed. A service the Service Control Manager does not know is refused, and a service whose start type is Disabled is refused with that named.",
		nil,
		[]collection.Example{
			{
				Name:        "Restart after a config change",
				RunbookYAML: "- name: Write the app's config\n  file.copy:\n    src: ./app.config\n    dest: C:\\Program Files\\App\\app.config\n  register: app_config\n\n- name: Restart the app service only if the config actually changed\n  svc.windows.restart:\n    name: AppService\n  when:\n    - app_config.changed\n",
			},
		},
		[]string{"svc.windows.start", "svc.windows.stop", "svc.restart"},
	)
}

// Restart implements "svc.windows.restart".
//
// converged is nil, the same choice svc.systemd.restart makes: a restart
// of a running service is not a no-op, so there is no state in which
// "already done" is true.
//
// inverse is nil for the reason the manifest states: no instruction
// reverses an interruption. The diff is still recorded by runServiceOp,
// so a rollback reaching this task can see what the service's state was.
// restartOp is svc.windows.restart's operation, shared by Restart and CheckRestart so the
// method and its check cannot disagree about it. A function rather than a
// variable, so it reads the service functions (startFunc and the rest) when
// it is called, which is what lets a test replace them.
func restartOp() serviceOp {
	return serviceOp{
		fqcn:            "svc.windows.restart",
		converged:       nil,
		apply:           restartFunc,
		refusesDisabled: true,
		inverse:         nil,
		predict: func(s winrmsvc.State) winrmsvc.State {
			s.Status = "Running"
			return s
		},
	}
}

func Restart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, restartOp(), collection.ModeExecute)
}

// CheckRestart is svc.windows.restart's check: the same Get-Service read, refusals
// and decision as Restart, then a prediction (Status Running) instead of the change.
func CheckRestart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runServiceOp(ctx, rc, device, params, restartOp(), collection.ModeCheck)
}

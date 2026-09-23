package systemd

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotesvc"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "svc.systemd.restart",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSystemd},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "A restart's effect is the interruption itself, and there is no instruction that un-interrupts " +
					"a service. Restarting it a second time would run this method again rather than reverse it. The " +
					"unit's state is deliberately still recorded under diff, so a rollback reaching this task can " +
					"see that the unit was running before and after and decide for itself, but this method emits no " +
					"instruction because none would be true.",
			},
			// A check reads the unit's state and reports the restart a real
			// run would send, without sending it. See CheckRestart.
			SupportsCheck: true,
			Doc:           restartDoc(),
		},
		Invoke: Restart,
		Check:  CheckRestart,
	})
}

func restartDoc() collection.Doc {
	return unitDoc(
		"Restarts a systemd unit, starting it if it was not running.",
		"Restarts a systemd unit. This is ansible.builtin.systemd with state=restarted, and like that module it starts a unit that was not running rather than failing. It is the one method in this namespace that is never converged: restarting a running unit is the point, not a no-op, so this always sends the command and always reports changed. That makes it the method most worth putting behind a when condition or a handler, so a service is only bounced when something it reads actually changed. A unit systemd does not know is refused, and a masked unit is refused with the mask named.",
		nil,
		[]collection.Example{
			{
				Name:        "Restart after a config change",
				RunbookYAML: "- name: Write the nginx config\n  file.copy:\n    src: ./nginx.conf\n    dest: /etc/nginx/nginx.conf\n  register: nginx_config\n\n- name: Restart nginx only if the config actually changed\n  svc.systemd.restart:\n    name: nginx\n  when:\n    - nginx_config.changed\n",
			},
		},
		[]string{"svc.systemd.start", "svc.systemd.stop", "svc.restart"},
	)
}

// Restart implements "svc.systemd.restart".
//
// converged is nil, which is what makes this the one method here that
// always acts. A restart of a running unit is not a no-op, so there is no
// state in which "already done" is true.
//
// inverse is nil for the reason the manifest states: no instruction
// reverses an interruption. The diff is still recorded by runUnitOp, so a
// rollback reaching this task can see what the unit's state was.
func Restart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, restartOp, collection.ModeExecute)
}

// restartOp is what makes svc.systemd.restart different from its
// siblings, shared by Restart and CheckRestart.
var restartOp = unitOp{
	fqcn:          "svc.systemd.restart",
	converged:     nil,
	apply:         remotesvc.Restart,
	predict:       predictActiveState(activeStateActive),
	refusesMasked: true,
	inverse:       nil,
}

// CheckRestart is "svc.systemd.restart" in check mode: it reads the unit
// and reports the restart a real run would send, sending nothing.
//
// It always predicts a change, because Restart always makes one: a
// restart is never converged, so a dry run that said "nothing to do" for
// a running unit would be describing a different method. The predicted
// after half is the unit with ActiveState "active", which is what
// systemctl restart leaves a unit that starts cleanly. A masked or
// unknown unit is refused exactly as Restart refuses it.
func CheckRestart(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runUnitOp(ctx, rc, device, params, restartOp, collection.ModeCheck)
}

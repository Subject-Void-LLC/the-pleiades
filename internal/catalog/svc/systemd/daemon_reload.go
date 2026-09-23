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
		Name: "svc.systemd.daemon_reload",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSystemd},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "Re-reading unit files has no prior state to restore. What systemd held before the reload was " +
					"a view of unit files that have since changed on disk, and nothing can ask it to go back to " +
					"believing the old contents. Undoing whatever wrote those files is that task's inverse, not " +
					"this one's.",
			},
			// A check connects and reports the reload a real run would
			// send, without sending it. See CheckDaemonReload.
			SupportsCheck: true,
			Doc:           daemonReloadDoc(),
		},
		Invoke: DaemonReload,
		Check:  CheckDaemonReload,
	})
}

func daemonReloadDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes systemd re-read every unit file on disk.",
		Description: "Runs systemctl daemon-reload, which makes systemd pick up unit files that were written, changed or removed since it last read them. This is ansible.builtin.systemd with daemon_reload=yes on its own. It is the step that makes a unit file an earlier task wrote visible to systemd at all: without it, svc.systemd.start on a brand new unit fails with the unit not found, which reads as a broken unit file rather than as a stale view. It takes no unit name, because it is not about one unit, and it always reports changed: systemd exposes no way to ask whether a reload would have made any difference, so claiming otherwise would be a guess.",
		Params: []collection.Param{
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: nil,
		Examples: []collection.Example{
			{
				Name:        "Install a unit file and make systemd see it",
				RunbookYAML: "- name: Write the unit file\n  file.copy:\n    src: ./app.service\n    dest: /etc/systemd/system/app.service\n    mode: \"0644\"\n\n- name: Make systemd re-read its unit files\n  svc.systemd.daemon_reload: {}\n\n- name: Start the new service\n  svc.systemd.start:\n    name: app\n",
			},
		},
		SeeAlso: []string{"svc.systemd.start", "svc.systemd.enable"},
	}
}

// DaemonReload implements "svc.systemd.daemon_reload".
//
// It does not go through runUnitOp, and that is deliberate rather than an
// oversight. That helper's whole shape is read a unit, compare, act,
// record what it was. This takes no unit, has no state to read, and has
// no inverse, so routing it through there would mean inventing all three.
//
// It always reports Changed true. systemd offers no way to ask whether a
// reload would have made any difference, so the honest options are
// "always changed" or a guess, and a guess here would be the kind that
// makes a handler silently stop firing.
func DaemonReload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return daemonReload(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDaemonReload is "svc.systemd.daemon_reload" in check mode.
//
// It predicts exactly what DaemonReload reports, a change, for the same
// reason DaemonReload reports one: systemd offers no way to ask whether a
// reload would make a difference, so the only answer that is not a guess
// is "a real run would reload". It still connects, so a dry run against
// a device that cannot be reached, or with a credential that is refused,
// fails the way the real run would rather than predicting a reload that
// could never be sent. It records nothing, as DaemonReload records
// nothing: there is no unit and no state to describe.
func CheckDaemonReload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return daemonReload(ctx, rc, device, params, collection.ModeCheck)
}

// daemonReload is the one body DaemonReload and CheckDaemonReload share.
// The two differ only in whether the reload is sent.
func daemonReload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "svc.systemd.daemon_reload"

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	if mode != collection.ModeCheck {
		if err := remotesvc.DaemonReload(ctx, conn); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: true}, nil
}

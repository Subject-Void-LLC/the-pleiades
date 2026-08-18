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
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "Re-reading unit files has no prior state to restore. What systemd held before the reload was " +
					"a view of unit files that have since changed on disk, and nothing can ask it to go back to " +
					"believing the old contents. Undoing whatever wrote those files is that task's inverse, not " +
					"this one's.",
			},
			Doc: daemonReloadDoc(),
		},
		Invoke: DaemonReload,
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
				RunbookYAML: "- name: Write the unit file\n  fqcn: file.copy\n  params:\n    src: ./app.service\n    dest: /etc/systemd/system/app.service\n    mode: \"0644\"\n\n- name: Make systemd re-read its unit files\n  fqcn: svc.systemd.daemon_reload\n  params: {}\n\n- name: Start the new service\n  fqcn: svc.systemd.start\n  params:\n    name: app\n",
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
	const fqcn = "svc.systemd.daemon_reload"

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	if err := remotesvc.DaemonReload(ctx, conn); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: true}, nil
}

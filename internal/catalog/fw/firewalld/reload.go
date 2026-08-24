package firewalld

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "fw.firewalld.reload",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameFirewalld},
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
			PlatformTargets:      nil,
			EngineVersion:        ">=1.0.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "A reload applies the permanent configuration to the runtime one, and there is no " +
					"instruction that un-applies it. Undoing whatever permanent rules were added or removed is " +
					"the task that changed them, not this one.",
			},
			Doc: reloadDoc(),
		},
		Invoke: Reload,
	})
}

func reloadDoc() collection.Doc {
	return collection.Doc{
		Summary: "Reloads firewalld to apply pending rule changes.",
		Description: "Runs firewall-cmd --reload, which applies the permanent configuration to the runtime " +
			"one and is what makes an fw.firewalld.allow or fw.firewalld.deny run with permanent: true, " +
			"immediate: false visible without a full firewalld restart. This is ansible.posix.firewalld's " +
			"own no-op-detecting reload, except firewall-cmd exposes no way to ask whether a reload would " +
			"have made any difference, so this always reports changed the same way " +
			"svc.systemd.daemon_reload does for the identical reason.",
		Params: []collection.Param{
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: nil,
		Examples: []collection.Example{
			{
				Name:        "Persist a rule and apply it now",
				RunbookYAML: "- name: Allow HTTPS permanently\n  fw.firewalld.allow:\n    port: 443\n    immediate: false\n\n- name: Apply it\n  fw.firewalld.reload: {}\n",
			},
		},
		SeeAlso: []string{"fw.firewalld.allow", "fw.firewalld.deny"},
	}
}

// Reload implements "fw.firewalld.reload".
//
// It always reports Changed true, for the same reason
// svc.systemd.daemon_reload does: firewalld offers no way to ask whether
// a reload would have made any difference, and guessing would be the
// kind of guess that makes a handler silently stop firing.
func Reload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "fw.firewalld.reload"

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	if err := runFirewallCmd(ctx, conn, []string{"firewall-cmd", "--reload"}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: true}, nil
}

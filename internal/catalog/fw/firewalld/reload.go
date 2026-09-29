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
			ExecutionContext:     collection.ExecutionContext{RequiresElevation: true, Site: collection.SiteTarget, Device: collection.DeviceRequired},
			PlatformTargets:      nil,
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			// A check reports the change a reload always reports, after the read that fails when a reload would (CheckReload).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes: "A reload applies the permanent configuration to the runtime one, and there is no " +
					"instruction that un-applies it. Undoing whatever permanent rules were added or removed is " +
					"the task that changed them, not this one.",
			},
			Doc: reloadDoc(),
		},
		Invoke: Reload,
		Check:  CheckReload,
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
	return reload(ctx, rc, device, params, collection.ModeExecute)
}

// CheckReload is fw.firewalld.reload's check: a reload always reports a change, since it replaces the runtime configuration with the permanent one, and what that replaces cannot be told without comparing both whole; so the check predicts that same change. It reads firewall-cmd --state first, which fails exactly when a reload would: firewalld not running.
func CheckReload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return reload(ctx, rc, device, params, collection.ModeCheck)
}

// reload is Reload's and CheckReload's one body; mode says which.
func reload(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = "fw.firewalld.reload"

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	if mode == collection.ModeCheck {
		if err := runFirewallCmd(ctx, conn, []string{"firewall-cmd", "--state"}); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		return collection.Result{Changed: true}, nil
	}

	if err := runFirewallCmd(ctx, conn, []string{"firewall-cmd", "--reload"}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	return collection.Result{Changed: true}, nil
}

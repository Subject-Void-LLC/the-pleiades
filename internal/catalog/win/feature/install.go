package feature

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmdism"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "win.feature.install",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameWindowsFeature,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: true,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that enabled a disabled feature emits a win.feature.remove naming it. A run that " +
					"found it already enabled emits nothing. What the inverse cannot undo is anything the feature's " +
					"own presence changed on the system while it was enabled, and either direction may need the " +
					"restart reboot_required reports before it is complete.",
			},
			Doc: installDoc(),
		},
		Invoke: Install,
	})
}

func installDoc() collection.Doc {
	return featureDoc(
		"Enables a Windows optional feature or role via DISM, including its required parent features.",
		"Makes sure a Windows optional feature or role is enabled. This is ansible.windows.win_optional_feature with state=present (or win_feature's default), built on dism.exe /online /enable-feature rather than the ServerManager PowerShell module, since dism.exe works on every Windows SKU and this platform's own DISMLogPath capability already commits to it. /all is passed, so enabling a feature also enables the parent features it requires, matching what the Windows GUI's own \"Add roles and features\" does by default. State is read before anything is sent, so a feature that is already enabled reports no change and no command reaches the device. A feature name DISM does not recognize is refused rather than reported as already enabled, since that is nearly always a typo. Many features need a restart before they finish taking effect; check reboot_required rather than assuming changed alone means the feature is fully usable.",
		[]collection.Example{
			{
				Name:        "Enable IIS",
				RunbookYAML: "- name: Make sure the web server role is enabled\n  fqcn: win.feature.install\n  params:\n    name: IIS-WebServerRole\n  register: iis\n\n- name: Reboot if DISM asked for one\n  fqcn: exec.winrm.shell\n  params:\n    shell: powershell\n    command: Restart-Computer -Force\n    expect_disconnect: true\n  when:\n    - iis.reboot_required\n",
			},
		},
		[]string{"win.feature.remove"},
	)
}

// Install implements "win.feature.install".
func Install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runFeatureOp(ctx, rc, device, params, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply:     enableFunc,
		inverse: func(name string) (sdk.Inverse, bool) {
			return sdk.Inverse{
				FQCN:   "win.feature.remove",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Disable %s, which this task enabled. It does not undo any restart the "+
					"change needed, and does not touch the parent features /all pulled in.", name),
			}, true
		},
	})
}

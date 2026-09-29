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
		Name: "win.feature.remove",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.NameWindowsFeature,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: true,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that disabled an enabled feature emits a win.feature.install naming it. A run that " +
					"found it already disabled emits nothing. Re-enabling it does not pass /all a second time from " +
					"this inverse, so a parent feature the original install pulled in stays exactly as removing " +
					"this one left it.",
				Inverses: []sdk.InverseSpec{
					{FQCN: "win.feature.install", Record: []string{"name"}},
				},
			},
			Doc: removeDoc(),
			// A check reads the feature and sends nothing (predictFeature).
			SupportsCheck: true,
		},
		Invoke: Remove,
		Check:  CheckRemove,
	})
}

func removeDoc() collection.Doc {
	return featureDoc(
		"Disables a Windows optional feature or role via DISM.",
		"Makes sure a Windows optional feature or role is disabled. This is ansible.windows.win_optional_feature with state=absent, built on dism.exe /online /disable-feature. Unlike install, this does not pass /all: removing a feature should not silently remove the parent features it depended on. State is read before anything is sent, so a feature that is already disabled reports no change. A feature name DISM does not recognize is refused rather than reported as already disabled, since that is nearly always a typo. Many features need a restart before removal fully takes effect; check reboot_required rather than assuming changed alone means the feature is gone. A check reads the feature and sends nothing; when the feature would change, its diff leaves out the state the feature would end in and reboot_required, since DISM decides between the finished and pending states only when it runs.",
		[]collection.Example{
			{
				Name:        "Disable IIS",
				RunbookYAML: "- name: Make sure the web server role is disabled\n  win.feature.remove:\n    name: IIS-WebServerRole\n",
			},
		},
		[]string{"win.feature.install"},
	)
}

// Remove implements "win.feature.remove".
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runFeatureOp(ctx, rc, device, params, removeOp(), collection.ModeExecute)
}

// CheckRemove is "win.feature.remove"'s check: it reads the feature and
// says whether Remove would change it, sending nothing.
func CheckRemove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runFeatureOp(ctx, rc, device, params, removeOp(), collection.ModeCheck)
}

// removeOp is Remove's operation. It is built on each call rather than
// held in a package variable, so it reads disableFunc when it runs and a
// test that swaps that seam is obeyed (FAILURE_PATTERNS 259).
func removeOp() featureOp {
	return featureOp{
		fqcn:      "win.feature.remove",
		converged: winrmdism.FeatureState.DisabledState,
		apply:     disableFunc,
		inverse: func(name string) (sdk.Inverse, bool) {
			return sdk.Inverse{
				FQCN:   "win.feature.install",
				Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Enable %s again, which this task disabled. It does not undo any restart "+
					"the change needed.", name),
			}, true
		},
	}
}

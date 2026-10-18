// win.capability.remove: a Windows capability made absent, as SYSTEM.
package capability

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
		Name: "win.capability.remove",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"winrm",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("WindowsFeatureCapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: true,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that removed the capability emits a win.capability.install naming it, which fetches it " +
					"from Windows Update again: one first installed from a source needs that source in its rollback: " +
					"list. One found absent, or pending a restart to finish going, emits nothing.",
				Inverses: []sdk.InverseSpec{{FQCN: fqcnInstall, Record: []string{paramName}}},
			},
			// A check reads the capability and sends nothing.
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Removes a Windows capability (a feature on demand) with DISM, run as SYSTEM.",
				Description: "Makes sure a Windows capability, what Get-WindowsCapability lists, is not installed. Its state is read first with dism.exe /get-capabilityinfo, so a capability that is not present, only staged, or removed and waiting only for a restart reports no change and nothing is sent; a name DISM does not recognize is refused. The removal runs dism.exe as SYSTEM in a one-shot scheduled task in the \\Pleiades\\ folder, as win.capability.install does, which this method registers, waits for and removes. Many capabilities need a restart to finish going, which reboot_required says; win.reboot restarts the host. A run that removed one emits win.capability.install naming it, which fetches it from Windows Update again: a capability first installed from a source needs that source named in its rollback: list. A check reads the capability and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The capability's whole identity as DISM lists it, with its four tildes and version, such as ServerCore.AppCompatibility~~~~0.0.1.0 or Language.Basic~~~en-US~0.0.1.0 (Get-WindowsCapability -Online lists them). A name DISM does not recognize is refused rather than reported as already in the asked state, since that is nearly always a typo."},
					{Name: "timeout", Type: "int", Default: "3600", Description: "How many seconds DISM may run. Task Scheduler stops it at that limit and the task fails saying so."},
				},
				Returns: []collection.ReturnField{
					{Name: "name", Type: "string", Returned: "always", Description: "The capability this task acted on."},
					{Name: "reboot_required", Type: "bool", Returned: "always", Description: "Whether a restart is needed to finish: DISM answered 3010, or the capability was found already waiting for one. win.reboot restarts the host."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "What DISM reported about the capability before this task and after it, each holding name, exists and state. Recorded even on a run that changed nothing."},
				},
				Examples: []collection.Example{
					{Name: "Remove Steps Recorder", RunbookYAML: "- name: Remove Steps Recorder\n  win.capability.remove:\n    name: App.StepsRecorder~~~~0.0.1.0\n"},
				},
				SeeAlso: []string{"win.capability.install", "win.reboot", "win.feature.remove"},
			},
		},
		Invoke: Remove,
		Check:  CheckRemove,
	})
}

// Remove implements "win.capability.remove".
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCapabilityOp(ctx, rc, device, params, removeOp(), collection.ModeExecute)
}

// CheckRemove is its check: it reads the capability and says whether
// Remove would change it, sending nothing.
func CheckRemove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCapabilityOp(ctx, rc, device, params, removeOp(), collection.ModeCheck)
}

// removeOp is Remove's operation, built on each call so a test that
// replaces removeFunc is obeyed (FAILURE_PATTERNS 259).
func removeOp() capabilityOp {
	remove := removeFunc
	return capabilityOp{
		fqcn:    fqcnRemove,
		done:    winrmdism.CapabilityState.Removed,
		pending: winrmdism.CapabilityState.RemovePending,
		apply: func(ctx context.Context, session winrmdism.Session, logPath string, r request) (winrmdism.ChangeResult, error) {
			return remove(ctx, session, logPath, r.name, r.timeout)
		},
		inverse: func(name string) sdk.Inverse {
			return sdk.Inverse{FQCN: fqcnInstall, Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Install %s again, which this task removed, from Windows Update. It may need a restart to finish.", name)}
		},
	}
}

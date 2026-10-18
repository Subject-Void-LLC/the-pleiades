// win.capability.install: a Windows capability made present, as SYSTEM.
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

// The two methods' names, each the other's undo.
const (
	fqcnInstall = "win.capability.install"
	fqcnRemove  = "win.capability.remove"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "win.capability.install",
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
				Notes: "A run that installed the capability emits a win.capability.remove naming it; one that found it " +
					"installed, or pending a restart to finish installing, emits nothing. Either direction may need " +
					"the restart reboot_required reports before it is complete.",
				Inverses: []sdk.InverseSpec{{FQCN: fqcnRemove, Record: []string{paramName}}},
			},
			// A check reads the capability and sends nothing.
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Installs a Windows capability (a feature on demand) with DISM, run as SYSTEM so Windows Update will serve it.",
				Description: "Makes sure a Windows capability, what Get-WindowsCapability lists and Windows calls a feature on demand, is installed: ServerCore.AppCompatibility, OpenSSH.Server and the like. Its state is read first with dism.exe /get-capabilityinfo, so a capability already installed, or installed and waiting only for a restart, reports no change and nothing is sent; a name DISM does not recognize is refused. The payload comes from Windows Update unless source names a folder or mounted image holding the host's features on demand, and then Windows Update is left alone. The Windows Update agent refuses a WinRM logon (Add-WindowsCapability over WinRM answers Access is denied), so the install runs dism.exe as SYSTEM in a one-shot scheduled task in the \\Pleiades\\ folder, which this method registers, waits for and removes. A large capability from Windows Update can take many minutes: ServerCore.AppCompatibility took 23 on a one-CPU host. Many capabilities need a restart to finish, which reboot_required says, both on the run that installed one and on a later run that finds it waiting; win.reboot restarts the host. A run that installed it emits win.capability.remove naming it. A check reads the capability and sends nothing.",
				Params: []collection.Param{
					{Name: "name", Type: "string", Required: true, Description: "The capability's whole identity as DISM lists it, with its four tildes and version, such as ServerCore.AppCompatibility~~~~0.0.1.0 or Language.Basic~~~en-US~0.0.1.0 (Get-WindowsCapability -Online lists them). A name DISM does not recognize is refused rather than reported as already in the asked state, since that is nearly always a typo."},
					{Name: "source", Type: "string", Description: "A folder or mounted image on the host holding its features on demand, such as D:\\LanguagesAndOptionalFeatures, used instead of Windows Update. It may not hold a %, a quote or a line break."},
					{Name: "timeout", Type: "int", Default: "3600", Description: "How many seconds DISM may run. Task Scheduler stops it at that limit and the task fails saying so."},
				},
				Returns: []collection.ReturnField{
					{Name: "name", Type: "string", Returned: "always", Description: "The capability this task acted on."},
					{Name: "reboot_required", Type: "bool", Returned: "always", Description: "Whether a restart is needed to finish: DISM answered 3010, or the capability was found already waiting for one. win.reboot restarts the host."},
					{Name: "diff", Type: "dict", Returned: "always", Description: "What DISM reported about the capability before this task and after it, each holding name, exists and state. Recorded even on a run that changed nothing."},
				},
				Examples: []collection.Example{
					{Name: "Make Server Core run more applications", RunbookYAML: "- name: Install Server Core's application compatibility feature\n  win.capability.install:\n    name: ServerCore.AppCompatibility~~~~0.0.1.0\n  register: appcompat\n\n- name: Restart to finish it\n  win.reboot: {}\n  when:\n    - result.appcompat.reboot_required\n"},
				},
				SeeAlso: []string{"win.capability.remove", "win.reboot", "win.feature.install"},
			},
		},
		Invoke: Install,
		Check:  CheckInstall,
	})
}

// Install implements "win.capability.install".
func Install(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCapabilityOp(ctx, rc, device, params, installOp(), collection.ModeExecute)
}

// CheckInstall is its check: it reads the capability and says whether
// Install would change it, sending nothing.
func CheckInstall(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runCapabilityOp(ctx, rc, device, params, installOp(), collection.ModeCheck)
}

// installOp is Install's operation, built on each call so a test that
// replaces addFunc is obeyed (FAILURE_PATTERNS 259).
func installOp() capabilityOp {
	add := addFunc
	return capabilityOp{
		fqcn:    fqcnInstall,
		source:  true,
		done:    winrmdism.CapabilityState.Installed,
		pending: winrmdism.CapabilityState.InstallPending,
		apply: func(ctx context.Context, session winrmdism.Session, logPath string, r request) (winrmdism.ChangeResult, error) {
			return add(ctx, session, logPath, r.name, r.source, r.timeout)
		},
		inverse: func(name string) sdk.Inverse {
			return sdk.Inverse{FQCN: fqcnRemove, Params: map[string]any{paramName: name},
				Description: fmt.Sprintf("Remove %s, which this task installed. It may need a restart to finish.", name)}
		},
	}
}

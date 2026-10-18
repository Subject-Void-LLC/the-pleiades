// This file holds the methods that prepare a Windows host itself: its
// capabilities (features on demand) and a restart, reached over WinRM.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// capabilityNameDoc, capabilityTimeoutDoc and the returns are shared by
// win.capability.install and remove, which differ only in direction.
const (
	capabilityNameDoc    = "The capability's whole identity as DISM lists it, with its four tildes and version, such as ServerCore.AppCompatibility~~~~0.0.1.0 or Language.Basic~~~en-US~0.0.1.0 (Get-WindowsCapability -Online lists them). A name DISM does not recognize is refused rather than reported as already in the asked state, since that is nearly always a typo."
	capabilityTimeoutDoc = "How many seconds DISM may run. Task Scheduler stops it at that limit and the task fails saying so."
)

var capabilityReturns = []collection.ReturnField{
	{Name: "name", Type: "string", Returned: "always", Description: "The capability this task acted on."},
	{Name: "reboot_required", Type: "bool", Returned: "always", Description: "Whether a restart is needed to finish: DISM answered 3010, or the capability was found already waiting for one. win.reboot restarts the host."},
	{Name: "diff", Type: "dict", Returned: "always", Description: "What DISM reported about the capability before this task and after it, each holding name, exists and state. Recorded even on a run that changed nothing."},
}

var winHostCollections = []collectionscaffold.Config{
	{
		Name:              "win.capability.install",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Installs a Windows capability (a feature on demand) with DISM, run as SYSTEM so Windows Update will serve it.",
			Description: "Makes sure a Windows capability, what Get-WindowsCapability lists and Windows calls a feature on demand, is installed: ServerCore.AppCompatibility, OpenSSH.Server and the like. Its state is read first with dism.exe /get-capabilityinfo, so a capability already installed, or installed and waiting only for a restart, reports no change and nothing is sent; a name DISM does not recognize is refused. The payload comes from Windows Update unless source names a folder or mounted image holding the host's features on demand, and then Windows Update is left alone. The Windows Update agent refuses a WinRM logon (Add-WindowsCapability over WinRM answers Access is denied), so the install runs dism.exe as SYSTEM in a one-shot scheduled task in the \\Pleiades\\ folder, which this method registers, waits for and removes. A large capability from Windows Update can take many minutes: ServerCore.AppCompatibility took 23 on a one-CPU host. Many capabilities need a restart to finish, which reboot_required says, both on the run that installed one and on a later run that finds it waiting; win.reboot restarts the host. A run that installed it emits win.capability.remove naming it. A check reads the capability and sends nothing.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: capabilityNameDoc},
				{Name: "source", Type: "string", Description: "A folder or mounted image on the host holding its features on demand, such as D:\\LanguagesAndOptionalFeatures, used instead of Windows Update. It may not hold a %, a quote or a line break."},
				{Name: "timeout", Type: "int", Default: "3600", Description: capabilityTimeoutDoc},
			},
			Returns: capabilityReturns,
			Examples: []collection.Example{
				{Name: "Make Server Core run more applications", RunbookYAML: "- name: Install Server Core's application compatibility feature\n  win.capability.install:\n    name: ServerCore.AppCompatibility~~~~0.0.1.0\n  register: appcompat\n\n- name: Restart to finish it\n  win.reboot: {}\n  when:\n    - result.appcompat.reboot_required\n"},
			},
			SeeAlso: []string{"win.capability.remove", "win.reboot", "win.feature.install"},
		},
	},
	{
		Name:              "win.capability.remove",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a Windows capability (a feature on demand) with DISM, run as SYSTEM.",
			Description: "Makes sure a Windows capability, what Get-WindowsCapability lists, is not installed. Its state is read first with dism.exe /get-capabilityinfo, so a capability that is not present, only staged, or removed and waiting only for a restart reports no change and nothing is sent; a name DISM does not recognize is refused. The removal runs dism.exe as SYSTEM in a one-shot scheduled task in the \\Pleiades\\ folder, as win.capability.install does, which this method registers, waits for and removes. Many capabilities need a restart to finish going, which reboot_required says; win.reboot restarts the host. A run that removed one emits win.capability.install naming it, which fetches it from Windows Update again: a capability first installed from a source needs that source named in its rollback: list. A check reads the capability and sends nothing.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: capabilityNameDoc},
				{Name: "timeout", Type: "int", Default: "3600", Description: capabilityTimeoutDoc},
			},
			Returns: capabilityReturns,
			Examples: []collection.Example{
				{Name: "Remove Steps Recorder", RunbookYAML: "- name: Remove Steps Recorder\n  win.capability.remove:\n    name: App.StepsRecorder~~~~0.0.1.0\n"},
			},
			SeeAlso: []string{"win.capability.install", "win.reboot", "win.feature.remove"},
		},
	},
	{
		Name:              "win.reboot",
		Capabilities:      []capability.Name{capability.NameWinRM},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Restarts a Windows host over WinRM and waits until it is back, as ansible.windows.win_reboot does.",
			Description: "Reads the host's last boot time, has shutdown.exe restart it pre_reboot_delay seconds later (closing applications that do not exit, and recorded as a planned restart for maintenance), then waits until the host answers over WinRM again with a later boot time, which is what shows it restarted rather than only answered, and then post_reboot_delay seconds more. reboot_timeout bounds the wait from the moment the restart is asked for; a try the host refuses, or does not answer within connect_timeout, is made again five seconds later. A restart already scheduled on the host is cancelled and replaced by this one. A run always reports a change, and a restart cannot be undone. A check reads the boot time and sends nothing.",
			Params: []collection.Param{
				{Name: "reboot_timeout", Type: "int", Default: "600", Description: "How many seconds the host may take to come back, from the moment the restart is asked for."},
				{Name: "pre_reboot_delay", Type: "int", Default: "2", Description: "How many seconds shutdown.exe waits before restarting, at least 0 and at most 600."},
				{Name: "post_reboot_delay", Type: "int", Default: "0", Description: "How many seconds to wait once the host is back, for services that start after WinRM does."},
				{Name: "msg", Type: "string", Default: "Reboot initiated by The Pleiades", Description: "The message shutdown.exe shows signed-in users and records, at most 512 characters on one line."},
				{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try to reach the host may take."},
			},
			Returns: []collection.ReturnField{
				{Name: "rebooted", Type: "bool", Returned: "always", Description: "Whether the host restarted: true on a run, false in a check."},
				{Name: "elapsed", Type: "int", Returned: "on a run", Description: "How many seconds the restart took, from asking for it to the host being back."},
				{Name: "previous_boot_time", Type: "string", Returned: "always", Description: "When the host last booted before this task, in UTC (RFC 3339)."},
				{Name: "boot_time", Type: "string", Returned: "on a run", Description: "When the host booted this time, in UTC (RFC 3339)."},
			},
			Examples: []collection.Example{
				{Name: "Restart after a change that needs it", RunbookYAML: "- name: Install Server Core's application compatibility feature\n  win.capability.install:\n    name: ServerCore.AppCompatibility~~~~0.0.1.0\n  register: appcompat\n\n- name: Restart to finish it\n  win.reboot:\n    reboot_timeout: 900\n  when:\n    - result.appcompat.reboot_required\n"},
			},
			SeeAlso: []string{"win.capability.install", "win.feature.install", "wait.connection"},
		},
	},
}

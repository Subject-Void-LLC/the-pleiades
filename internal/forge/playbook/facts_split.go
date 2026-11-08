// Package playbook: a filtered setup task, split by device.
//
// Ansible runs ansible.builtin.setup on a Windows host as ansible.windows'
// setup.ps1: a WinRM, PSRP or PowerShell-over-SSH connection prefers .ps1
// modules (ansible-core 2.17.1, plugins/connection/winrm.py:245, psrp.py:344,
// ssh.py:583), the action plugin asks for the module of that type
// (plugins/action/__init__.py:236,263), and the loader routes setup.ps1 to
// ansible.windows.setup (plugins/loader.py:458-464,
// config/ansible_builtin_runtime.yml:4101-4102). That module has no filter
// option and adds any argument it does not know to its spec as raw before
// Ansible.Basic checks for unsupported ones (ansible.windows 2.4.0,
// setup.ps1:13-40), and never reads it: on a Windows host the filter is
// ignored and every fact gathered. So a filtered setup converts as two
// facts.gather tasks: the filtered one where the device is not
// WindowsShellCapable, and one without the filter, its subsets kept, where it
// is. Each reads the device root's capabilities (internal/engine's
// device_root.go), which needs no gather, and the engine decides each on
// every device on its own. Exactly one runs on each device, so both take the
// task's register: a skipped task registers nothing (the engine returns before
// it records a result, executor.go), so a later read sees the one that ran.
//
// The gap: a Windows host this platform does not know as
// WindowsShellCapable (a generic_ssh device whose onboarding never granted
// it) gets the filtered gather, where Ansible would gather everything.
package playbook

import (
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// windowsCondition is the condition, over the device root, that holds on a
// WindowsShellCapable device.
var windowsCondition = strconv.Quote(string(capability.NameWindowsShell)) + " in device.capabilities"

// splitByWindows is c split by device: c itself for a device that is not
// WindowsShellCapable, and c with the parameters windows for one that is.
func splitByWindows(c plannedCall, windows map[string]any) []plannedCall {
	filtered := c
	filtered.label, filtered.when = "not Windows", "!("+windowsCondition+")"
	whole := plannedCall{call: c.call, params: windows, label: "Windows", when: windowsCondition}
	return []plannedCall{filtered, whole}
}

// unfiltered is params without setup's filter, for the Windows half of a
// split: the subsets and every other parameter kept.
func unfiltered(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		if k != "filter" {
			out[k] = v
		}
	}
	return out
}

// This file holds the Packages section of docs/hephaestus.md's catalog:
// ansible.builtin.package, ansible.builtin.apt, and ansible.builtin.dnf.
// All target side, over SSH, all requiring elevation to install, remove,
// or upgrade a system package.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var packagesCollections = []collectionscaffold.Config{
	{
		Name:              "pkg.install",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a package is present, whichever package manager the device runs.",
			Description: "Makes sure a package is installed, without caring which package manager the device uses. This is ansible.builtin.package with state=present: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.install and behaves exactly as that method does, including reporting no change when the package is already present at the requested version. Use the concrete method instead when a runbook is written for one platform and should say so.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The package to act on. This is passed straight through to the concrete method for the device's package manager, so it means whatever that manager means by a package name: an APT package name on a Debian-family host, an RPM package name on a Red Hat-family one."},
				{Name: "version", Type: "string", Description: "Pin to this exact version instead of whatever the package manager considers current. Passed straight through to the concrete method; leave unset to mean \"whatever version is current.\""},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on, as recorded by the concrete method that ran."},
				{Name: "version", Type: "string", Returned: "always", Description: "The version left installed after this task, empty when the package is absent afterward. The exact value comes from the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the package manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a package differs between package managers."},
			},
			Examples: []collection.Example{
				{Name: "Install a package without naming the package manager", RunbookYAML: "- name: Make sure curl is installed\n  pkg.install:\n    name: curl\n"},
			},
			SeeAlso: []string{"pkg.remove", "pkg.upgrade", "pkg.apt.install", "pkg.dnf.install"},
		},
	},
	{
		Name:              "pkg.remove",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a package is absent, whichever package manager the device runs.",
			Description: "Makes sure a package is removed, without caring which package manager the device uses. This is ansible.builtin.package with state=absent: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.remove and behaves exactly as that method does, including reporting no change when the package is already absent. Use the concrete method instead when a runbook is written for one platform and should say so.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The package to act on. This is passed straight through to the concrete method for the device's package manager, so it means whatever that manager means by a package name: an APT package name on a Debian-family host, an RPM package name on a Red Hat-family one."},
				{Name: "version", Type: "string", Description: "Pin to this exact version instead of whatever the package manager considers current. Passed straight through to the concrete method; leave unset to mean \"whatever version is current.\""},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on, as recorded by the concrete method that ran."},
				{Name: "version", Type: "string", Returned: "always", Description: "The version left installed after this task, empty when the package is absent afterward. The exact value comes from the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the package manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a package differs between package managers."},
			},
			Examples: []collection.Example{
				{Name: "Remove a package without naming the package manager", RunbookYAML: "- name: Make sure telnet is not installed\n  pkg.remove:\n    name: telnet\n"},
			},
			SeeAlso: []string{"pkg.install", "pkg.upgrade", "pkg.apt.remove", "pkg.dnf.remove"},
		},
	},
	{
		Name:              "pkg.upgrade",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure the newest available version of a package is installed, whichever package manager the device runs.",
			Description: "Makes sure a package is at its newest available version, without caring which package manager the device uses. This is ansible.builtin.package with state=latest for one named package, not a full-system upgrade: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.upgrade and behaves exactly as that method does, including installing the package fresh when it is absent and reporting no change when it is already current. Use the concrete method instead when a runbook is written for one platform and should say so.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The package to act on. This is passed straight through to the concrete method for the device's package manager, so it means whatever that manager means by a package name: an APT package name on a Debian-family host, an RPM package name on a Red Hat-family one."},
				{Name: "version", Type: "string", Description: "Pin to this exact version instead of whatever the package manager considers current. Passed straight through to the concrete method; leave unset to mean \"whatever version is current.\""},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on, as recorded by the concrete method that ran."},
				{Name: "version", Type: "string", Returned: "always", Description: "The version left installed after this task, empty when the package is absent afterward. The exact value comes from the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the package manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a package differs between package managers."},
			},
			Examples: []collection.Example{
				{Name: "Keep a package current without naming the package manager", RunbookYAML: "- name: Keep openssl at its newest available version\n  pkg.upgrade:\n    name: openssl\n"},
			},
			SeeAlso: []string{"pkg.install", "pkg.remove", "pkg.apt.upgrade", "pkg.dnf.upgrade"},
		},
	},
	{
		Name:              "pkg.apt.install",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a package is installed via APT.",
			Description: "Makes sure a package is present on a Debian-family host, installing it if it is absent. This is ansible.builtin.apt with state=present. Installed state is read from dpkg before anything is sent, so a package that is already present at the requested version (or present at any version, when none is requested) reports no change and no command reaches the device. version pins to an exact version string, the same way apt-get install name=version does; leave it unset to mean whatever apt-get would install unpinned.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The APT package name to install, such as curl or nginx."},
				{Name: "version", Type: "string", Description: "Install exactly this version rather than whatever is current. A package already installed at a different version is upgraded or downgraded to match."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on."},
				{Name: "version", Type: "string", Returned: "always", Description: "The version left installed after this task."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What dpkg reported about the package before this task and after it, each holding installed and version. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Install a package", RunbookYAML: "- name: Make sure curl is installed\n  pkg.apt.install:\n    name: curl\n"},
				{Name: "Pin an exact version", RunbookYAML: "- name: Install a specific nginx build\n  pkg.apt.install:\n    name: nginx\n    version: 1.24.0-2ubuntu7\n"},
			},
			SeeAlso: []string{"pkg.apt.remove", "pkg.apt.upgrade", "pkg.dnf.install", "pkg.install"},
		},
	},
	{
		Name:              "pkg.apt.remove",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a package via APT.",
			Description: "Makes sure a package is absent from a Debian-family host, removing it if it is present. This is ansible.builtin.apt with state=absent. Installed state is read from dpkg before anything is sent, so a package that is already absent reports no change and no command reaches the device. This removes the package but does not purge it (apt-get remove, not apt-get purge), so its configuration files are left on disk; there is no separate purge parameter today.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The APT package name to remove."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on."},
				{Name: "version", Type: "string", Returned: "always", Description: "Always empty after a successful run: a removed package has no installed version, and a package that was already absent had none either."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What dpkg reported about the package before this task and after it, each holding installed and version."},
			},
			Examples: []collection.Example{
				{Name: "Remove a package", RunbookYAML: "- name: Make sure telnet is not installed\n  pkg.apt.remove:\n    name: telnet\n"},
			},
			SeeAlso: []string{"pkg.apt.install", "pkg.apt.upgrade", "pkg.dnf.remove", "pkg.remove"},
		},
	},
	{
		Name:              "pkg.apt.upgrade",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a package is at its newest available version via APT.",
			Description: "Makes sure one named package is at the newest version APT knows about, upgrading it if a newer one is available. This is ansible.builtin.apt with state=latest for a single package, not apt-get upgrade or apt-get dist-upgrade: it never touches any package other than the one named. A package that is absent is installed fresh, since there is no \"current\" version to upgrade from. A package that is already at the newest candidate version reports no change and no command reaches the device.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The APT package to keep current."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on."},
				{Name: "version", Type: "string", Returned: "always", Description: "The version left installed after this task."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What dpkg reported about the package before this task and after it, each holding installed and version."},
			},
			Examples: []collection.Example{
				{Name: "Keep a package at its newest available version", RunbookYAML: "- name: Keep openssl current\n  pkg.apt.upgrade:\n    name: openssl\n"},
			},
			SeeAlso: []string{"pkg.apt.install", "pkg.apt.remove", "pkg.dnf.upgrade", "pkg.upgrade"},
		},
	},
	{
		Name:              "pkg.dnf.install",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a package is installed via DNF.",
			Description: "Makes sure a package is present on a Red Hat-family host, installing it if it is absent. This is ansible.builtin.dnf with state=present. Installed state is read from rpm before anything is sent, so a package that is already present at the requested version (or present at any version, when none is requested) reports no change and no command reaches the device. version pins to an exact version-release string, appended to the package name the way dnf's own name-version syntax expects (name-version, e.g. nginx-1.24.0); leave it unset to mean whatever dnf would install unpinned.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The RPM package name to install, such as curl or nginx."},
				{Name: "version", Type: "string", Description: "Install exactly this version-release rather than whatever is current. A package already installed at a different version is upgraded or downgraded to match."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on."},
				{Name: "version", Type: "string", Returned: "always", Description: "The version-release left installed after this task."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What rpm reported about the package before this task and after it, each holding installed and version. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Install a package", RunbookYAML: "- name: Make sure curl is installed\n  pkg.dnf.install:\n    name: curl\n"},
				{Name: "Pin an exact version", RunbookYAML: "- name: Install a specific nginx build\n  pkg.dnf.install:\n    name: nginx\n    version: 1.24.0-1.el9\n"},
			},
			SeeAlso: []string{"pkg.dnf.remove", "pkg.dnf.upgrade", "pkg.apt.install", "pkg.install"},
		},
	},
	{
		Name:              "pkg.dnf.remove",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a package via DNF.",
			Description: "Makes sure a package is absent from a Red Hat-family host, removing it if it is present. This is ansible.builtin.dnf with state=absent. Installed state is read from rpm before anything is sent, so a package that is already absent reports no change and no command reaches the device.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The RPM package name to remove."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on."},
				{Name: "version", Type: "string", Returned: "always", Description: "Always empty after a successful run: a removed package has no installed version, and a package that was already absent had none either."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What rpm reported about the package before this task and after it, each holding installed and version."},
			},
			Examples: []collection.Example{
				{Name: "Remove a package", RunbookYAML: "- name: Make sure telnet is not installed\n  pkg.dnf.remove:\n    name: telnet\n"},
			},
			SeeAlso: []string{"pkg.dnf.install", "pkg.dnf.upgrade", "pkg.apt.remove", "pkg.remove"},
		},
	},
	{
		Name:              "pkg.dnf.upgrade",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a package is at its newest available version via DNF.",
			Description: "Makes sure one named package is at the newest version DNF knows about, upgrading it if a newer build is available. This is ansible.builtin.dnf with state=latest for a single package, not a full-system dnf upgrade: it never touches any package other than the one named. A package that is absent is installed fresh, since there is no \"current\" version to upgrade from. A package that is already at the newest available build reports no change and no command reaches the device.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The RPM package to keep current."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The package this task acted on."},
				{Name: "version", Type: "string", Returned: "always", Description: "The version-release left installed after this task."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What rpm reported about the package before this task and after it, each holding installed and version."},
			},
			Examples: []collection.Example{
				{Name: "Keep a package at its newest available version", RunbookYAML: "- name: Keep openssl current\n  pkg.dnf.upgrade:\n    name: openssl\n"},
			},
			SeeAlso: []string{"pkg.dnf.install", "pkg.dnf.remove", "pkg.apt.upgrade", "pkg.upgrade"},
		},
	},
}

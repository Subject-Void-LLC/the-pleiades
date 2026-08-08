// This file holds the Packages section of docs/hephaestus.md's catalog:
// ansible.builtin.package, ansible.builtin.apt, and ansible.builtin.dnf.
// All target side, over SSH, all requiring elevation to install, remove,
// or upgrade a system package.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

var packagesCollections = []collectionscaffold.Config{
	{
		Name:              "pkg.install",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Installs a package using the target's own package manager, whichever it is."},
	},
	{
		Name:              "pkg.remove",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a package using the target's own package manager, whichever it is."},
	},
	{
		Name:              "pkg.upgrade",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Upgrades a package using the target's own package manager, whichever it is."},
	},
	{
		Name:              "pkg.apt.install",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Installs a package via APT."},
	},
	{
		Name:              "pkg.apt.remove",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a package via APT."},
	},
	{
		Name:              "pkg.apt.upgrade",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Upgrades a package via APT."},
	},
	{
		Name:              "pkg.dnf.install",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Installs a package via DNF."},
	},
	{
		Name:              "pkg.dnf.remove",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a package via DNF."},
	},
	{
		Name:              "pkg.dnf.upgrade",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Upgrades a package via DNF."},
	},
}

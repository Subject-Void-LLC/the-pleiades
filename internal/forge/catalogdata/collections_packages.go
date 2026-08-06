// This file holds the Packages section of docs/hephaestus.md's catalog:
// ansible.builtin.package, ansible.builtin.apt, and ansible.builtin.dnf.
// All target side, over SSH, all requiring elevation to install, remove,
// or upgrade a system package.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

var packagesCollections = []collectionscaffold.Config{
	{
		Name:              "pkg.install",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.remove",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.upgrade",
		Capabilities:      []capability.Name{capability.NamePackageManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.apt.install",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.apt.remove",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.apt.upgrade",
		Capabilities:      []capability.Name{capability.NameApt},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.dnf.install",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.dnf.remove",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "pkg.dnf.upgrade",
		Capabilities:      []capability.Name{capability.NameDnf},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
}

// This file holds the Extended infrastructure section of
// docs/hephaestus.md's catalog: ansible.posix.firewalld/mount,
// ansible.windows.win_feature, community.general.archive/unarchive,
// community.docker.docker_container, and the two amazon.aws modules. The
// two cloud entries declare no transport at all: they run against an API,
// not an inventory device over SSH, matching docs/hephaestus.md's own
// "clearest controller side cases in the catalog" description.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

var extendedCollections = []collectionscaffold.Config{
	{
		Name:              "fw.firewalld.allow",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "fw.firewalld.deny",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "fw.firewalld.reload",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "fs.mount",
		Capabilities:      []capability.Name{capability.NameLinux},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "fs.unmount",
		Capabilities:      []capability.Name{capability.NameLinux},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "win.feature.install",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "win.feature.remove",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:          "archive.create",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "archive.extract",
		Capabilities:  []capability.Name{capability.NameFileTransfer},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:              "container.docker.run",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "container.docker.stop",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "container.docker.remove",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:          "cloud.aws.ec2.create",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
	},
	{
		Name:          "cloud.aws.ec2.terminate",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
	},
	{
		Name:          "cloud.aws.s3.create_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
	},
	{
		Name:          "cloud.aws.s3.delete_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
	},
}

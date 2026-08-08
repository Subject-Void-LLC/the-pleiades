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
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

var extendedCollections = []collectionscaffold.Config{
	{
		Name:              "fw.firewalld.allow",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Opens a port or service in firewalld."},
	},
	{
		Name:              "fw.firewalld.deny",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Closes a port or service in firewalld."},
	},
	{
		Name:              "fw.firewalld.reload",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Reloads firewalld to apply pending rule changes."},
	},
	{
		Name:              "fs.mount",
		Capabilities:      []capability.Name{capability.NameLinux},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Mounts a filesystem on the target, and optionally persists it to fstab."},
	},
	{
		Name:              "fs.unmount",
		Capabilities:      []capability.Name{capability.NameLinux},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Unmounts a filesystem on the target, and optionally removes it from fstab."},
	},
	{
		Name:              "win.feature.install",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Installs a Windows feature or role."},
	},
	{
		Name:              "win.feature.remove",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a Windows feature or role."},
	},
	{
		Name:          "archive.create",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates an archive (tar or zip) from files on the target."},
	},
	{
		Name:          "archive.extract",
		Capabilities:  []capability.Name{capability.NameFileTransfer},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Extracts an archive (tar or zip) on the target."},
	},
	{
		Name:              "container.docker.run",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Runs a Docker container on the target."},
	},
	{
		Name:              "container.docker.stop",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Stops a running Docker container on the target."},
	},
	{
		Name:              "container.docker.remove",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a Docker container from the target."},
	},
	{
		Name:          "cloud.aws.ec2.create",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates an EC2 instance via the AWS API."},
	},
	{
		Name:          "cloud.aws.ec2.terminate",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Terminates an EC2 instance via the AWS API."},
	},
	{
		Name:          "cloud.aws.s3.create_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates an S3 bucket via the AWS API."},
	},
	{
		Name:          "cloud.aws.s3.delete_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Deletes an S3 bucket via the AWS API."},
	},
}

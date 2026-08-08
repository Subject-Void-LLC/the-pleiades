// This file holds the Files and configuration section of
// docs/hephaestus.md's catalog: ansible.builtin.copy, .template, .file,
// .lineinfile, and .blockinfile. None of these require elevation by
// default, mirroring Ansible's own file/copy/lineinfile modules, which run
// as the connecting user unless a runbook separately elevates.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var filesCollections = []collectionscaffold.Config{
	{
		Name:          "file.copy",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Copies a file to the target, from a local source or inline content."},
	},
	{
		Name:          "file.template",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Renders a template and writes the result to the target."},
	},
	{
		Name:          "file.directory",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates or removes a directory on the target, recursively if needed."},
	},
	{
		Name:          "file.symlink",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates a symbolic link on the target."},
	},
	{
		Name:          "file.remove",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Removes a file or directory from the target."},
	},
	{
		Name:          "file.touch",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates an empty file on the target, or updates its modification time."},
	},
	{
		Name:          "file.permissions",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Sets a file's owner, group, and mode on the target."},
	},
	{
		Name:          "file.line.set",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Ensures one line matching a pattern is present in a file, replacing or appending it."},
	},
	{
		Name:          "file.line.remove",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Ensures no line matching a pattern remains in a file."},
	},
	{
		Name:          "file.block.set",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Ensures a marked, multi-line block of text is present in a file."},
	},
	{
		Name:          "file.block.remove",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Removes a marked, multi-line block of text from a file."},
	},
}

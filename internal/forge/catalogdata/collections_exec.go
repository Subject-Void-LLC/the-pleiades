// This file holds the Execution section of docs/hephaestus.md's catalog:
// ansible.builtin.command and ansible.builtin.shell.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var execCollections = []collectionscaffold.Config{
	{
		Name:          "exec.command",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Runs one command directly, with no shell involved."},
	},
	{
		Name:          "exec.shell",
		Capabilities:  []capability.Name{capability.NameShellExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Runs a command through the target's shell, so pipes and redirects work."},
	},
}

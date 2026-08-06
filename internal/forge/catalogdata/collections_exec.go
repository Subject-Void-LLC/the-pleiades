// This file holds the Execution section of docs/hephaestus.md's catalog:
// ansible.builtin.command and ansible.builtin.shell.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

var execCollections = []collectionscaffold.Config{
	{
		Name:          "exec.command",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "exec.shell",
		Capabilities:  []capability.Name{capability.NameShellExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
}

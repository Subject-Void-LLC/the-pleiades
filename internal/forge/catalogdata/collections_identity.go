// This file holds the Identity section of docs/hephaestus.md's catalog:
// ansible.builtin.user and ansible.builtin.group.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var identityCollections = []collectionscaffold.Config{
	{
		Name:              "identity.user.create",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Creates a POSIX user account on the target."},
	},
	{
		Name:              "identity.user.remove",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a POSIX user account from the target."},
	},
	{
		Name:              "identity.user.modify",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Modifies an existing POSIX user account on the target."},
	},
	{
		Name:              "identity.group.create",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Creates a POSIX group on the target."},
	},
	{
		Name:              "identity.group.remove",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a POSIX group from the target."},
	},
	{
		Name:              "identity.group.modify",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Modifies an existing POSIX group on the target."},
	},
}

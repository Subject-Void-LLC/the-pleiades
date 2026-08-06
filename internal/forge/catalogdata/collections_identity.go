// This file holds the Identity section of docs/hephaestus.md's catalog:
// ansible.builtin.user and ansible.builtin.group.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

var identityCollections = []collectionscaffold.Config{
	{
		Name:              "identity.user.create",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "identity.user.remove",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "identity.user.modify",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "identity.group.create",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "identity.group.remove",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "identity.group.modify",
		Capabilities:      []capability.Name{capability.NamePosixAccount},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
}

// This file holds the Gating and facts section of docs/hephaestus.md's
// catalog: ansible.builtin.uri/wait_for/setup. http.request declares no
// capability at all, matching the catalog table's own "none" entry: an
// arbitrary HTTP call has no device-side prerequisite to check.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

var gatingCollections = []collectionscaffold.Config{
	{
		Name:          "http.request",
		EngineVersion: engineVersion,
	},
	{
		Name:          "wait.port",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "wait.path",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "wait.search",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "facts.gather",
		Capabilities:  []capability.Name{capability.NameFactGatherer},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
}

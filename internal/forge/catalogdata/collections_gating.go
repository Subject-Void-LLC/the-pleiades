// This file holds the Gating and facts section of docs/hephaestus.md's
// catalog: ansible.builtin.uri/wait_for/setup. http.request declares no
// capability at all, matching the catalog table's own "none" entry: an
// arbitrary HTTP call has no device-side prerequisite to check.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var gatingCollections = []collectionscaffold.Config{
	{
		Name:          "http.request",
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Makes an HTTP request and reports its status code and body."},
	},
	{
		// Namespaced under pleiades.builtin, not the bare "wait.port" its
		// wait.path/wait.search siblings still use: this is the first
		// (and, as of this entry, only) native-only method in the catalog
		// deliberately marked as belonging to Pleiades' own reserved
		// namespace rather than mapping 1:1 from an Ansible module name.
		// See docs/hephaestus.md's "four of the thirty six are not
		// collections at all" section for set_metadata's sibling case
		// (internal/engine/action.go), and note this rename intentionally
		// leaves wait.path/wait.search un-renamed rather than expanding
		// scope to "fix" the now-visible inconsistency among the three.
		Name:          "pleiades.builtin.wait.port",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Waits for a TCP port on the target to start (or stop) accepting connections."},
	},
	{
		Name:          "wait.path",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Waits for a file path on the target to exist (or stop existing)."},
	},
	{
		Name:          "wait.search",
		Capabilities:  []capability.Name{capability.NameNetworkAddressable},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Waits for a pattern to appear in a file's contents on the target."},
	},
	{
		Name:          "facts.gather",
		Capabilities:  []capability.Name{capability.NameFactGatherer},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Gathers baseline system facts from the target (OS, kernel, distribution)."},
	},
}

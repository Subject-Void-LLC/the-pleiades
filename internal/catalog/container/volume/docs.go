// The container.volume.* methods' documentation, kept equal to
// internal/forge/catalogdata's copy by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package volume

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// Shared parameters and returns.
var (
	nameParam = collection.Param{Name: paramName, Type: "string", Required: true, Description: "The volume's name."}

	nameReturn      = collection.ReturnField{Name: statName, Type: "string", Returned: "always", Description: "The volume's name."}
	createdAtReturn = collection.ReturnField{Name: statCreatedAt, Type: "string", Returned: "when the volume exists", Description: "When the engine made the volume, as it reports it."}
)

// createDoc is container.volume.create's documentation.
func createDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes a named volume exist as declared.",
		Description: "Makes the volume named name exist on the device's engine, with the local driver and the declared labels. An absent volume is created; one that matches is no change; one whose driver or a declared label differs is refused, naming each difference, and never replaced, since that would lose its data. Driver options are not offered: a local volume's mount options can carry a password. Records removing the volume as the undo, pinned to the moment the engine made it, so the undo never removes another volume made under the name since. A check reads the volume and creates nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramDriver, Type: "string", Default: driverLocal, Choices: drivers, Description: "The volume driver: local, the engine's own, which keeps the volume's data on the host."},
			{Name: paramLabels, Type: "dict", Description: "Labels on the volume. Labels it has beyond these are allowed."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn,
			{Name: statDriver, Type: "string", Returned: "always", Description: "The volume's driver."},
			createdAtReturn,
		},
		Examples: []collection.Example{
			{Name: "A volume for a database's data", RunbookYAML: "- name: Database data\n  container.volume.create:\n    name: pgdata\n    labels:\n      app: shop\n"},
		},
		SeeAlso: []string{"container.volume.remove", "container.volume.info", "container.run"},
	}
}

// removeDoc is container.volume.remove's documentation.
func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes a volume no container uses.",
		Description: "Removes the volume named name and its data; an absent one is no change. A volume any container mounts, running or stopped, is refused, naming them. With created_at, a volume the engine made at another moment is refused, so an undo never removes another volume made under the name since. A removal has no undo: the data is gone. A check reads the volume and the containers using it, and removes nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramCreatedAt, Type: "string", Description: "When the engine made the volume, as container.volume.create returned it, which the volume named must still have."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn, createdAtReturn,
			{Name: statRemoved, Type: "bool", Returned: "when the volume existed", Description: "Whether the volume was removed (by a check, would be)."},
		},
		Examples: []collection.Example{{Name: "Remove a scratch volume", RunbookYAML: "- name: Remove the scratch volume\n  container.volume.remove:\n    name: scratch\n"}},
		SeeAlso:  []string{"container.volume.create", "container.remove"},
	}
}

// infoDoc is container.volume.info's documentation.
func infoDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Reads a volume.",
		Description: "Reads the volume named name as the engine reports it: its driver, mount point on the host, scope, labels, when it was made, and its driver options by name only, since a local volume's mount options can carry a password. An absent volume is reported as exists false rather than refused, so a runbook can branch on it. Reading changes nothing, and a check reads the same way." + containerengine.ReachNote,
		Params:      []collection.Param{nameParam, containerengine.HostKeyParam},
		Fragments:   []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn,
			{Name: statExists, Type: "bool", Returned: "always", Description: "Whether the volume exists."},
			{Name: statDriver, Type: "string", Returned: "when the volume exists", Description: "The volume's driver."},
			{Name: statMountpoint, Type: "string", Returned: "when the volume exists", Description: "Where the volume's data is on the host."},
			{Name: statScope, Type: "string", Returned: "when the volume exists", Description: "The volume's scope: local, or global."},
			createdAtReturn,
			{Name: statLabels, Type: "dict", Returned: "when the volume exists", Description: "The volume's labels."},
			{Name: statOptionNames, Type: "list of string", Returned: "when the volume exists", Description: "The names of the volume's driver options. Their values are never reported."},
		},
		Examples: []collection.Example{
			{Name: "Read a volume a later task branches on", RunbookYAML: "- name: Read pgdata\n  container.volume.info:\n    name: pgdata\n  register: pgdata\n"},
		},
		SeeAlso: []string{"container.volume.create", "container.network.info"},
	}
}

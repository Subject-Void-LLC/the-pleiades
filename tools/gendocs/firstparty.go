// Package main: the first-party lists the core ships (Phase 136a), as data a
// reader, a later core and tools/schemacompat can compare release by release.
package main

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// firstPartyDocument is first-party.json: the groups, the namespaces and fact
// floor they reserve, which group's methods fill each fact prefix, and the
// routing table from each built-in method to its group.
type firstPartyDocument struct {
	// SchemaVersion is this document's own version.
	SchemaVersion int `json:"schema_version"`
	// Groups are the first-party Collections, core ones marked.
	Groups []collection.FirstPartyGroup `json:"groups"`
	// Namespaces are the method namespaces no external Collection may use.
	Namespaces []string `json:"namespaces"`
	// FactFloor are the fact prefixes no external Collection may declare into.
	FactFloor []string `json:"fact_floor"`
	// FactFamilyOwners names, for each fact floor prefix, the groups whose
	// methods declare facts inside it.
	FactFamilyOwners map[string][]string `json:"fact_family_owners"`
	// Methods routes each built-in method to its group.
	Methods map[string]string `json:"methods"`
}

// generateFirstParty emits outDir/schemas/first-party.json and the identical
// copy internal/api/wellknown embeds, from pkg/collection's static lists and
// this build's catalog (moduleCatalogEntries).
func generateFirstParty(outDir string) error {
	manifests, err := moduleCatalogEntries()
	if err != nil {
		return err
	}
	routes, err := collection.FirstPartyRoutes(manifests)
	if err != nil {
		return err
	}
	return writeSchema(outDir, "first-party.json", firstPartyDocument{
		SchemaVersion:    1,
		Groups:           collection.FirstPartyGroups(),
		Namespaces:       collection.FirstPartyNamespaces(),
		FactFloor:        collection.FactFloor(),
		FactFamilyOwners: collection.FactFamilyOwners(manifests),
		Methods:          routes,
	})
}

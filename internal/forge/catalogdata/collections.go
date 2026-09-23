// This file aggregates every catalogdata collection-section slice into the
// one exported Collections value tools/gencatalog drives the real pleiades
// forge new-collection CLI from, and holds the single engine version
// constraint every generated manifest shares today.
package catalogdata

import "github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"

// engineVersion is the minimum core engine version constraint recorded on
// every generated Collection manifest. Every entry shares the one value
// collectionscaffold declares, rather than each section inventing its own
// or this file restating it: a second copy is a second thing to move when
// the release line does.
const engineVersion = collectionscaffold.DefaultEngineVersion

// Collections is every native Collection method docs/hephaestus.md's
// catalog table describes, in the exact shape
// internal/forge/collectionscaffold.Generate expects. It is the single
// source of truth tools/gencatalog reads to drive the real `pleiades forge
// new-collection` command once per entry: editing the catalog means
// editing this data, then re-running `go generate ./internal/forge/catalogdata`,
// never hand-editing anything under internal/catalog/.
var Collections = concatCollections(
	execCollections,
	packagesCollections,
	servicesCollections,
	identityCollections,
	filesCollections,
	networkCollections,
	extendedCollections,
	gatingCollections,
)

// concatCollections joins every catalog section slice into one, explicit
// and reviewable rather than relying on init() order or a package-level
// append chain that would silently reorder if a section were added later.
func concatCollections(sections ...[]collectionscaffold.Config) []collectionscaffold.Config {
	var all []collectionscaffold.Config
	for _, s := range sections {
		all = append(all, s...)
	}
	return all
}

// Package hostfacts: the fact families a method declares it reads back after a
// change, as facts.gather declares them, so every method that may emit one of
// these facts declares it with the gatherer's own kind and type.
package hostfacts

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// ServiceFactSpecs are the service families: the service stable, its state
// volatile, since it moves on its own.
func ServiceFactSpecs() []collection.FactSpec {
	return []collection.FactSpec{
		{Key: ServiceFamily, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
			Description: "A service as its manager lists it after the change: enabled, status and source."},
		{Key: ServiceStateFamily, Prefix: true, ItemNames: true, Kind: collection.FactVolatile, Type: collection.FactString,
			Description: "A service's state after the change, kept as its latest value only."},
	}
}

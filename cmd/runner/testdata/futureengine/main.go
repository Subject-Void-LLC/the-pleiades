// Package main is futureengine, an external Collection whose one method
// states that it needs a Pleiades release far newer than any build. It
// exists only for cmd/runner's version Release Gate, which proves the
// Runner and the CLI hand the loader the same stamped version: a release
// build refuses this program, and a development build loads it with one
// warning.
//
// It lives under testdata/ so `go build ./...` and `go vet ./...` never
// see it. The gate builds it by path.
package main

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// main hands the method to external.Main.
func main() {
	external.Main(collection.Descriptor{
		Name: "gatefixture.future.run",
		Manifest: collection.Manifest{
			EngineVersion: ">=99.0.0",
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture that does nothing"},
		},
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
	})
}

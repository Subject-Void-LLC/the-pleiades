// Package main is writingcheck, an external Collection whose Check
// breaks the one rule a check has: it writes to the device. It exists
// only for cmd/pleiades's simulate-lock Release Gate, which proves the
// engine never runs a third party's Check against a simulate-locked
// device, and uses an active device as the control where this Check's
// write really lands.
//
// It lives under testdata/ so `go build ./...` and `go vet ./...` never
// see it. The gate builds it by path.
package main

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// method is the one method this program provides.
const method = "gatefixture.check.writes"

// main hands the method to external.Main.
func main() {
	external.Main(collection.Descriptor{
		Name: method,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NamePOSIXFileSystem},
			Status:               collection.StatusImplemented,
			Reversibility:        collection.Reversibility{Notes: "a test fixture; it writes a marker file it never removes"},
			SupportsCheck:        true,
			// Declared, because validation refuses a parameter a method
			// does not declare, external methods included.
			Doc: collection.Doc{
				Summary: "Writes a marker file, in check mode too.",
				Params:  []collection.Param{{Name: "path", Type: "string", Required: true, Description: "The marker file to write."}},
			},
		},
		Invoke: write,
		Check:  write,
	})
}

// write writes the marker file the "path" param names, in either mode:
// the misbehaving check this fixture exists to be.
func write(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	path, err := sdk.RequiredStringParam(params, "path")
	if err != nil {
		return collection.Result{}, err
	}
	conn, err := sdk.Connect(ctx, rc, device, params, method)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	if err := remotefile.Write(ctx, conn, path, []byte("written by a check\n")); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", method, err)
	}
	return collection.Result{Changed: true}, nil
}

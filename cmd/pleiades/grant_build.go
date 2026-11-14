// Command pleiades's `revoke-capability --build`: taking back every grant
// one program build probed, once that build is known to be bad.
package main

import (
	"context"
	"fmt"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// revokeByBuild revokes, on every device of dir's inventory, each probed
// grant whose recorded build (inventory.Grant.Build) is build, and keeps
// every other grant: another build's, and every assertion, which no build
// made. A grant probed by a bad build is evidence only that build gave, so
// the admin who learns the build was bad takes back all of it at once, by
// the digest `pleiades collection list` shows, and onboards again with a
// good build. It fails when build is not a digest or matched nothing, so a
// mistyped digest is never reported as done.
func revokeByBuild(dir, build string) error {
	if !inventory.IsBuildDigest(build) {
		return fmt.Errorf("--build %q is not a program build's digest (sha256:<64 lower case hex>, as pleiades collection list shows it)", build)
	}
	ctx := context.Background()
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(inventoryFile(dir), factory)
	items, err := loadInventory(dir)
	if err != nil {
		return err
	}
	revoked := 0
	for _, listed := range items {
		grants, _, err := inventory.GrantsFrom(listed.Properties())
		if err != nil {
			return fmt.Errorf("host %q: %w", listed.Name(), err)
		}
		var bad []inventory.Grant
		for _, g := range grants {
			if g.Source == inventory.GrantProbed && g.Build == build {
				bad = append(bad, g)
			}
		}
		if len(bad) == 0 {
			continue
		}
		// Read again through the repository that saves it, so the write is
		// checked against the version it was read at.
		item, err := repo.GetByName(ctx, listed.Name())
		if err != nil {
			return err
		}
		g, ok := item.(granting)
		if !ok {
			return fmt.Errorf("host %q holds grants but cannot revoke them", item.Name())
		}
		for _, b := range bad {
			if _, err := g.RevokeGrant(b.Capability); err != nil {
				return fmt.Errorf("host %q: %w", item.Name(), err)
			}
		}
		if err := factory.Rebuild(item); err != nil {
			return fmt.Errorf("host %q not changed: %w", item.Name(), err)
		}
		if err := repo.Save(ctx, item); err != nil {
			return err
		}
		for _, b := range bad {
			fmt.Printf("revoked %s from host %q, probed by build %s\n", b.Capability, item.Name(), build)
			revoked++
		}
	}
	if revoked == 0 {
		return fmt.Errorf("no device holds a grant probed by build %s", build)
	}
	return nil
}

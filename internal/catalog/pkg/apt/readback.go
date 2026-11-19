// Package apt: the post-change reading (Phase 41c). After a real change, the
// method reads the package back from dpkg with facts.gather's own query and
// emits the same package.<name> fact the gatherer stores (pkg/hostfacts), so a
// journal entry that changed state has its observed result beside it rather
// than waiting for the next gather, and the next gather reads it as unchanged.
package apt

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// packageReadBackSpec is the package fact family a method that changes a
// package declares it reads back: stable objects, one per package, as
// facts.gather declares the same family, so the two store one fact.
func packageReadBackSpec() collection.FactSpec {
	return collection.FactSpec{Key: hostfacts.PackageFamily, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
		Description: "One installed package as dpkg holds it after the change: version, arch and source."}
}

// readBack emits the package facts of name as dpkg holds it now: one per
// architecture dpkg lists it under. The change is already made and its undo
// recorded, so a read that fails is returned as a warning, never the task's
// failure (FAILURE_PATTERNS 452's rule).
func readBack(ctx context.Context, rc sdk.RunbookContext, conn *remoteexec.Conn, name string) []string {
	result, err := conn.Run(ctx, "dpkg-query -W -f='"+hostfacts.DpkgFormat+"' -- "+remoteexec.QuoteArg(name))
	if err != nil {
		return []string{fmt.Sprintf("changed %s, but could not read its package fact back: %v", name, err)}
	}
	if result.ExitCode != 0 {
		return []string{fmt.Sprintf("changed %s, but dpkg-query exited %d reading its package fact back", name, result.ExitCode)}
	}
	var warnings []string
	for _, p := range hostfacts.ParseDpkg(result.Stdout) {
		if err := rc.EmitFact(hostfacts.PackageKey(p), hostfacts.PackageValue(p)); err != nil {
			warnings = append(warnings, fmt.Sprintf("changed %s, but its package fact was not emitted: %v", name, err))
		}
	}
	return warnings
}

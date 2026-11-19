// Package dnf: the post-change reading (Phase 41c). After a real change, the
// method reads the package back from rpm with facts.gather's own format and
// emits the same package.<name> fact the gatherer stores (pkg/hostfacts), so a
// journal entry that changed state has its observed result beside it and the
// next gather reads it as unchanged.
package dnf

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// rpmNotInstalled is rpm -q's exit for a name it holds no package under.
const rpmNotInstalled = 1

// packageReadBackSpec is the package fact family a method that changes a
// package declares it reads back, as facts.gather declares the same family.
func packageReadBackSpec() collection.FactSpec {
	return collection.FactSpec{Key: hostfacts.PackageFamily, Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject,
		Description: "One installed package as rpm holds it after the change: version, arch and source."}
}

// readBack emits the package facts of name as rpm holds it now. A name dnf
// resolved to another package (vim to vim-enhanced) holds none under its own
// name, which is not a failure: nothing is emitted, and the next gather stores
// the package under the name it has. Any other failed read is a warning, never
// the task's failure, since the change is already made.
func readBack(ctx context.Context, rc sdk.RunbookContext, conn *remoteexec.Conn, name string) []string {
	result, err := conn.Run(ctx, "LC_ALL=C rpm -q --qf '"+hostfacts.RPMFormat+"' -- "+remoteexec.QuoteArg(name))
	if err != nil {
		return []string{fmt.Sprintf("changed %s, but could not read its package fact back: %v", name, err)}
	}
	switch result.ExitCode {
	case 0:
	case rpmNotInstalled:
		return nil
	default:
		return []string{fmt.Sprintf("changed %s, but rpm -q exited %d reading its package fact back", name, result.ExitCode)}
	}
	var warnings []string
	for _, p := range hostfacts.ParseRPM(result.Stdout) {
		if err := rc.EmitFact(hostfacts.PackageKey(p), hostfacts.PackageValue(p)); err != nil {
			warnings = append(warnings, fmt.Sprintf("changed %s, but its package fact was not emitted: %v", name, err))
		}
	}
	return warnings
}

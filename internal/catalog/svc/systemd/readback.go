// Package systemd: the post-change reading (Phase 41c). After a real change,
// the method runs facts.gather's own systemd listings and emits the changed
// unit's service.<unit> and service_state.<unit> facts exactly as the next
// gather would store them (pkg/hostfacts), so a journal entry that changed
// state has its observed result beside it.
package systemd

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// listedName is the name systemd lists a unit under: the unit as named, with
// .service added when it names no type, as systemctl itself reads it.
func listedName(unit string) string {
	if strings.Contains(unit, ".") {
		return unit
	}
	return unit + ".service"
}

// readBack emits the changed unit's service facts as the gatherer's listings
// show it now. A unit those listings leave out (not a service, or one that
// lasts only as long as a login or a connection) emits nothing. The change is
// already made, so a read that fails is a warning, never the task's failure.
func readBack(ctx context.Context, rc sdk.RunbookContext, conn *remoteexec.Conn, unit string) []string {
	mark := "; echo '" + hostfacts.ServiceSectionMark + "'; "
	result, err := conn.Run(ctx, strings.Join(hostfacts.SystemdListings, mark))
	if err != nil {
		return []string{fmt.Sprintf("changed %s, but could not read its service facts back: %v", unit, err)}
	}
	if err := hostfacts.CheckSystemd(result.Stdout); err != nil {
		return []string{fmt.Sprintf("changed %s, but systemd's listings did not answer as they always do: %v", unit, err)}
	}
	key := hostfacts.ItemKey(hostfacts.ServiceFamily, listedName(unit))
	var warnings []string
	for _, f := range hostfacts.ParseSystemd(result.Stdout) {
		if f.Key != key {
			continue
		}
		for _, part := range hostfacts.SplitServiceState(f) {
			if err := rc.EmitFact(part.Key, part.Value); err != nil {
				warnings = append(warnings, fmt.Sprintf("changed %s, but its fact %s was not emitted: %v", unit, part.Key, err))
			}
		}
	}
	return warnings
}

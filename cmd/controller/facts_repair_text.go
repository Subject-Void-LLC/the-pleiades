// This file is how `controller facts` reads to a person (facts_repair.go):
// the same model its --json prints, so the two cannot disagree. Every name
// the report carries was already escaped by the store (termsafe.Quote), and
// no line names a fact unless --show-fact-names asked for it.
package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factsreport"
)

// writeInspectText prints an inspection report.
func writeInspectText(w io.Writer, r factsreport.Report) {
	fmt.Fprintf(w, "Facts keys:   %s\n", suppliedKeysText(r.Keys))
	if r.Scope == factsreport.ScopeFleet {
		fmt.Fprintf(w, "Devices:      %d with stored facts read, %d need repair\n", r.Scanned, r.NeedsRepair)
		if len(r.Devices) == 0 {
			fmt.Fprintln(w, "Every device's stored history can be served.")
		}
	}
	for _, d := range r.Devices {
		fmt.Fprintln(w)
		writeDeviceText(w, d)
	}
}

// suppliedKeysText names the keys an inspection read with.
func suppliedKeysText(k factsreport.Keys) string {
	var parts []string
	if k.Current != "" {
		parts = append(parts, "current "+k.Current)
	}
	if k.Previous != "" {
		parts = append(parts, "previous "+k.Previous)
	}
	return strings.Join(parts, ", ")
}

// writeDeviceText prints one device's inspection.
func writeDeviceText(w io.Writer, d factsreport.Device) {
	fmt.Fprintf(w, "Device %s (id %s): %s\n", d.Device, d.ID, d.State)
	fmt.Fprintf(w, "  Stored:     %d facts (%d stable, %d volatile), %d coverages, %d observations\n",
		d.Rows.Total, d.Rows.Stable, d.Rows.Volatile, d.Coverages, d.Observations)
	for _, k := range d.KeyedBy {
		fmt.Fprintf(w, "  Keyed by:   %s (%s), %d rows\n", k.KeyID, k.Role, k.Rows)
	}
	if d.KeyedByMore > 0 {
		fmt.Fprintf(w, "  Keyed by:   %d more rows under other key identities\n", d.KeyedByMore)
	}
	for _, c := range d.Under {
		fmt.Fprintf(w, "  Under the %s key %s: %s\n", c.Role, c.KeyID, keyCheckText(c, d.Rows.Total))
	}
	if d.TruncateFrom > 0 {
		fmt.Fprintf(w, "  Truncate:   would cut at gather %d, removing it and every later one\n", d.TruncateFrom)
	}
	if len(d.Repairs) > 0 {
		fmt.Fprintf(w, "  Repairs:    %s\n", strings.Join(d.Repairs, ", "))
	}
	fmt.Fprintf(w, "  %s\n", d.Advice)
}

// keyCheckText says how a history read under one set of keys.
func keyCheckText(c factsreport.KeyCheck, rows int) string {
	if c.Verifies {
		return fmt.Sprintf("verifies, all %d rows", rows)
	}
	f := c.Failure
	where := fmt.Sprintf("row %d, position %d", f.Row, f.Position)
	if f.ChainIndex != nil {
		where += fmt.Sprintf(", chain index %d", *f.ChainIndex)
	} else {
		where += ", volatile"
	}
	where += fmt.Sprintf(", gather %d, first observed %s, keyed by %s", f.Observation, f.ObservedAt.Format(time.RFC3339), f.KeyID)
	if f.Fact != "" {
		where += ", fact " + f.Fact
	}
	return fmt.Sprintf("does not verify. First failure: %s at %s. %d of %d rows passed before it; %d rows open as the device's, %d do not",
		f.Kind, where, c.Checked, rows, c.Opened, c.Failed)
}

// writeRepairText prints what a repair did, or would do.
func writeRepairText(w io.Writer, r entstore.Removal) {
	// The repair's own verb, as it is described before and after.
	head := map[string]string{factsreport.RepairDiscard: "Discarded", factsreport.RepairTruncate: "Truncated"}[r.Repair]
	removed := "Removed:   "
	if !r.Committed {
		head, removed = "Would "+r.Repair, "Removes:   "
	}
	fmt.Fprintf(w, "%s the stored facts of %s (id %s), found %s:\n", head, r.Before.Device, r.Before.ID, r.Before.State)
	fmt.Fprintf(w, "  %s %d stored facts, %d coverages, %d observations\n", removed, r.Facts, r.Coverages, r.Observations)
	if r.Repair == factsreport.RepairTruncate {
		fmt.Fprintf(w, "  Kept:       %d stored facts, %d of them re-keyed under the current key\n", r.Kept, r.Rekeyed)
	}
	fmt.Fprintf(w, "  Digest:     %s under %s, of every removed row as it was stored\n", r.Digest, r.DigestKey)
	fmt.Fprintf(w, "  Recorded:   %s\n", r.Recorded)
	if !r.Committed {
		fmt.Fprintln(w, "Nothing was changed. Run it again with --confirm to do it. What it removes cannot be recovered afterwards except from a backup taken before.")
		return
	}
	fmt.Fprintln(w, "Done, and recorded in the activity stream. The device's next gather stores its facts under the current key.")
}

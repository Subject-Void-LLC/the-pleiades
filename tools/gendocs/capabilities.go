package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// generateCapabilities emits outDir/capabilities.md: every registered
// capability, its parent in the hierarchy if it has one, and which other
// capabilities structurally require it. Read directly from
// pkg/capability.All(), the same vocabulary a Collection's
// RequiredCapabilities is checked against at registration time, so this
// page cannot list a capability that does not really exist.
func generateCapabilities(outDir string) error {
	all := capability.All()

	byParent := map[string][]string{}
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, string(name))
	}
	sort.Strings(names)

	for _, n := range names {
		desc := all[capability.Name(n)]
		if desc.Parent != "" {
			byParent[string(desc.Parent)] = append(byParent[string(desc.Parent)], n)
		}
	}

	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Capability vocabulary\n\n")
	b.WriteString("What a device *can do*, not what it *is*. A Collection method declares which " +
		"capabilities it requires; a device advertises one by structurally implementing the matching Go " +
		"interface.\n\n")

	// Honesty paragraph, hand written rather than derived: the vocabulary
	// below is real and registered, but nothing validates a Collection
	// method's RequiredCapabilities against a target device.
	// internal/validate.CapabilityRule reads engine.ActionCapability, a
	// two-entry table holding only "ssh_exec" and "ios_backup", and skips
	// every other fqcn. Delete this paragraph when that is wired up, and not
	// before: without it this page promises a plan-time check that does not
	// run for any of the catalog's methods.
	b.WriteString("**Nothing compares these to your inventory before a run yet.** `pleiades validate` " +
		"checks a target device's capabilities for exactly two legacy action names, `ssh_exec` and " +
		"`ios_backup`. For every catalog FQCN the required capability is documentation only: a mismatch " +
		"surfaces during the run, not at plan time. See " +
		"[Implementation status](../01-start-here.md#implementation-status).\n\n")

	rows := make([][]string, 0, len(names))
	for _, n := range names {
		desc := all[capability.Name(n)]
		parent := "-"
		if desc.Parent != "" {
			parent = code(string(desc.Parent))
		}
		children := "-"
		if kids := byParent[n]; len(kids) > 0 {
			sort.Strings(kids)
			children = quoteList(kids)
		}
		rows = append(rows, []string{code(n), parent, children})
	}
	b.WriteString(table([]string{"Capability", "Parent", "Children"}, rows))
	b.WriteString(fmt.Sprintf("\n%d capabilities registered.\n", len(names)))

	return os.WriteFile(filepath.Join(outDir, "capabilities.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}

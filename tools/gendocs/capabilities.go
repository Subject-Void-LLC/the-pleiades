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
	// below is real and registered, and a method's RequiredCapabilities is
	// checked against its device before the method runs
	// (engine.checkMethodCapabilities), but not by pleiades validate, whose
	// CapabilityRule reads engine.ActionCapability, a table of the legacy
	// action names only. Transports are the exception since Phase 75:
	// validate's TransportRule checks them at plan time. Delete the
	// capability half when validate checks RequiredCapabilities too, and
	// not before.
	b.WriteString("**When these are checked.** A method's required capabilities are checked against " +
		"its device before the method runs, on the CLI and on a Runner, so a mismatch stops the task " +
		"rather than reaching the device. `pleiades validate` does not check them yet, except for the " +
		"legacy action names `ssh_exec` and `ios_backup`. A method's transports (below) are checked " +
		"at plan time: `pleiades validate` refuses a task whose device reaches none of them. See " +
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

	// The transport table, read from the same vocabulary collection.Register
	// checks a method's SupportedTransports against.
	b.WriteString("\n## Transports\n\n")
	b.WriteString("How a method's work reaches its device. A method lists the transports it uses; a device " +
		"reaches a transport when it has any one of the capabilities beside it. `pleiades validate`, and the " +
		"engine again before the method runs, refuse a task whose device reaches none of its method's " +
		"transports. A method that calls an API rather than its device lists none.\n\n")
	transportRows := make([][]string, 0, len(capability.Transports()))
	for _, transport := range capability.Transports() {
		reached, _ := capability.ReachedBy(transport)
		names := make([]string, 0, len(reached))
		for _, name := range reached {
			names = append(names, string(name))
		}
		transportRows = append(transportRows, []string{code(transport), quoteList(names)})
	}
	b.WriteString(table([]string{"Transport", "Reached by any of"}, transportRows))

	return os.WriteFile(filepath.Join(outDir, "capabilities.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}

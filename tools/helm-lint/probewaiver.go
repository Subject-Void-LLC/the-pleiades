// The one rule in this tool that reads Go source rather than a rendered
// manifest: proving that the written waiver letting a container ship without
// probes still describes the binary inside it.
//
// WHY THIS IS NOT A COMMENT. objects.go's probeWaivers holds a claim about the
// world, and claims about the world go stale. The runner's says the binary has
// no healthcheck subcommand, which is true today and is the entire reason the
// chart is allowed to render that container with no liveness probe. The day
// somebody adds one, the chart keeps shipping no probes, every test in the
// repository stays green, and the FOUND-NOT-FIXED failure this waiver stands
// in for (FAILURE_PATTERNS.md #119: a runner whose NATS connection closes for
// good stays alive, stays healthy-looking, and silently stops doing any work)
// stays open for no reason at all. Nobody re-reads a waiver they did not write.
//
// So the waiver names the condition and this file checks it, on every `make
// ci`, and fails with the chart edit spelled out.
//
// WHAT IT MATCHES ON, and why that is honest. It looks for a literal in the
// package's non-test .go files. That is text matching, which cannot see
// through Go semantics and is not being asked to: the question is not "what
// does this symbol mean" but "has a subcommand by this name appeared", and the
// cost of being wrong in the direction it can be wrong (a build failure naming
// a chart edit) is a conversation rather than a defect. Test files are skipped
// because a test that merely names the subcommand it wants would end the
// waiver before the subcommand exists.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// checkProbeWaiversStillHold reports every waiver whose stated reason has
// stopped describing the source it depends on.
//
// repoRoot is passed rather than read here so this stays a function of its
// arguments, the way every other rule in this tool is.
func checkProbeWaiversStillHold(repoRoot string) ([]finding, error) {
	var findings []finding

	// Sorted, so a run with more than one waiver reports them in a stable
	// order rather than in Go's randomized map order.
	names := make([]string, 0, len(probeWaivers))
	for name := range probeWaivers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		waiver := probeWaivers[name]
		dir := filepath.Join(repoRoot, waiver.sourceDir)
		found, err := sourceMentions(dir, waiver.endedBy)
		if err != nil {
			// Not a finding. A waiver whose source directory cannot be read
			// is this tool being broken, not the chart being wrong, and
			// reporting it as a chart finding would send somebody to edit
			// the wrong file.
			return nil, fmt.Errorf("checking whether the %q probe waiver still holds: %w", name, err)
		}
		if !found {
			continue
		}
		findings = append(findings, finding{
			profile: "probe waivers", object: waiver.sourceDir, container: name,
			message: fmt.Sprintf("%s now mentions %q, so the waiver letting this container ship without probes no longer describes it. %s\n      Recorded reason, now stale: %s",
				waiver.sourceDir, waiver.endedBy, waiver.remedy, waiver.reason),
		})
	}
	return findings, nil
}

// sourceMentions reports whether any non-test .go file directly in dir
// contains literal.
//
// Directly in dir, not recursively: a waiver names one package, and a
// subdirectory of it is a different package with its own reasons.
func sourceMentions(dir, literal string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- dir comes from probeWaivers, a fixed table in this package, joined with a directory entry it just listed.
		if err != nil {
			// A file that vanished between the listing and the read is not
			// evidence of anything; anything else is a real fault.
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		if strings.Contains(string(body), literal) {
			return true, nil
		}
	}
	return false, nil
}

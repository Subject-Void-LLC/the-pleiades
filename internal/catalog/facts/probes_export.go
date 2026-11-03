// Package facts: the probes as a test outside this package drives them
// against a real system (internal/catalog/facts/live, whose containers keep
// this package's own tests free of Docker).
package facts

import "strings"

// Probe is one probe facts.gather runs: the command it sends, and Read,
// which is what its parser makes of that command's output.
type Probe struct {
	// Command is the probe's fixed command (a PowerShell script on Windows).
	Command string
	parse   func(stdout string) []gatherFact
}

// Read is the facts the probe reads from stdout, by key, a key an earlier
// item took told apart as Gather tells it.
func (p Probe) Read(stdout string) map[string]any {
	names := gatherNames{}
	out := map[string]any{}
	for _, f := range p.parse(stdout) {
		f = names.distinct(f)
		out[f.key] = f.value
	}
	return out
}

// Probes is the probes facts.gather runs on platform (as uname -s names it,
// or Windows) for subset whose command holds text.
func Probes(platform, subset, text string) []Probe {
	var out []Probe
	for _, p := range gatherProbesFor(platform) {
		if p.subset == subset && strings.Contains(p.command, text) {
			out = append(out, Probe{Command: p.command, parse: p.parse})
		}
	}
	return out
}

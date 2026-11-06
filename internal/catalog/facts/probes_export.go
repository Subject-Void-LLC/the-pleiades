// Package facts: the probes as a test outside this package drives them
// against a real system (internal/catalog/facts/live, whose containers keep
// this package's own tests free of Docker).
package facts

import "strings"

// Probe is one probe facts.gather runs: the command it sends, and Read,
// which is what its parser makes of that command's output.
type Probe struct {
	// Command is the probe's command as Gather sends it (a PowerShell script
	// on Windows), a family's guarded by the test for its source.
	Command string
	parse   func(stdout string) []gatherFact
	family  bool
	check   func(stdout string) error
}

// Absent reports whether stdout says the probe's source is not on the
// system.
func (p Probe) Absent(stdout string) bool {
	outcome, _, _ := readOutcome(stdout, nil)
	return p.family && outcome == outcomeAbsent
}

// Read is the facts the probe reads from stdout, by key, a key an earlier
// item took told apart as Gather tells it. A family's answer is judged as
// Gather judges it first, and one that was absent or failed reads nothing.
func (p Probe) Read(stdout string) map[string]any {
	if p.family {
		outcome, body, _ := readOutcome(stdout, p.check)
		if outcome != outcomeRead {
			return map[string]any{}
		}
		stdout = body
	}
	names := gatherNames{}
	out := map[string]any{}
	for _, f := range p.parse(stdout) {
		if !p.family {
			out[f.key] = f.value
			continue
		}
		for _, item := range familyItems(names, f) {
			out[item.key] = item.value
		}
	}
	return out
}

// Probes is the probes facts.gather runs on platform (as uname -s names it,
// or Windows) for subset whose command holds text.
func Probes(platform, subset, text string) []Probe {
	var out []Probe
	for _, p := range gatherProbesFor(platform) {
		if p.subset == subset && strings.Contains(p.command, text) {
			command := p.command
			if p.present != "" {
				command = guarded(p.present, p.command)
			}
			out = append(out, Probe{Command: command, parse: p.parse, family: p.family != "", check: p.check})
		}
	}
	return out
}

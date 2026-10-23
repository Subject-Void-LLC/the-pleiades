// Package batfish: what Batfish parses without modelling, so a gate never
// passes over a feature the model cannot see.
package batfish

import (
	"regexp"
	"strings"
)

// unmodelled is a feature Batfish's parser recognises and its model does
// not use. Recognising a line says nothing of whether the data plane acts
// on it, so a line Batfish reads silently is as invisible to the model as
// one it reports unrecognised. Each entry was measured against the pinned
// Batfish before it was written here (Phase 127b, 2026-10-22), never
// assumed from documentation.
type unmodelled struct {
	// feature is the name a task accepts it by.
	feature string
	// format is the configuration format it is measured in.
	format string
	// line matches a configuration line that uses it.
	line *regexp.Regexp
	// why says what the model does instead.
	why string
}

// coverage is every unmodelled feature measured so far.
var coverage = []unmodelled{
	{"glbp", formatCiscoIOS, regexp.MustCompile(`^\s*glbp\s`), "Batfish parses GLBP and elects no forwarder: a GLBP virtual address is answered by nobody in the model"},
	{"ip_sla", formatCiscoIOS, regexp.MustCompile(`^ip sla\s`), "an IP SLA probe's result is runtime state; the model has none"},
	{"sla_tracking", formatCiscoIOS, regexp.MustCompile(`^track\s+\d+\s+ip\s+sla\s`), "the model treats an SLA track as always up, so a route or priority it guards never fails over in a scenario"},
	{"bfd", formatCiscoIOS, regexp.MustCompile(`^\s*bfd\s`), "BFD changes how fast a failure is detected, and the model is steady state"},
}

// unmodelledIn returns, for each unmodelled feature config uses, the first
// line number (from 1) and text using it.
func unmodelledIn(format, config string) map[string]lineUse {
	out := map[string]lineUse{}
	for i, line := range strings.Split(config, "\n") {
		for _, u := range coverage {
			if u.format != format || !u.line.MatchString(line) {
				continue
			}
			if _, seen := out[u.feature]; !seen {
				out[u.feature] = lineUse{line: i + 1, text: strings.TrimSpace(line)}
			}
		}
	}
	return out
}

// lineUse is where a configuration uses a feature.
type lineUse struct {
	line int
	text string
}

// coverageWhy returns why feature is not modelled.
func coverageWhy(feature string) string {
	for _, u := range coverage {
		if u.feature == feature {
			return u.why
		}
	}
	return ""
}

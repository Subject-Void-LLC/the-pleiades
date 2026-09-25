// Package playbook: the report's text view. It renders the same Report
// the JSON view serializes, so the two cannot disagree.
package playbook

import (
	"fmt"
	"strings"
)

// String renders r for a terminal: each runbook, each finding by outcome,
// the counts, and the tasks that cannot take part in check mode. Every
// piece of playbook text in r was escaped when the report was built.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "migrated %s\n\n", r.Playbook)
	for _, rb := range r.Runbooks {
		state := "runnable"
		if !rb.Runnable {
			state = "INCOMPLETE: resolve every blocked finding before it can run"
		}
		fmt.Fprintf(&b, "  %s (hosts: %s, plays %s): %s\n", rb.File, orNone(rb.Hosts), joinInts(rb.Plays), state)
	}
	for _, o := range []Outcome{OutcomeBlocked, OutcomeReview, OutcomeInfo} {
		var lines []string
		for _, f := range r.Findings {
			if f.Outcome != o {
				continue
			}
			line := fmt.Sprintf("  %s %s  %s: %s", f.ID, f.At, f.Code, f.Message)
			if f.Task != "" {
				line += fmt.Sprintf(" (task %q)", f.Task)
			}
			if f.Native != "" {
				line += "\n      instead: " + f.Native
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\n%s (%d):\n%s\n", o, len(lines), strings.Join(lines, "\n"))
		}
	}
	if len(r.Resolutions) > 0 {
		b.WriteString("\nvariables written into the runbook from the playbook's own definitions (a -e extra variable at run time would have overridden them):\n")
		for _, res := range r.Resolutions {
			fmt.Fprintf(&b, "  %s, defined at %s\n", res.Variable, res.DefinedAt)
		}
	}
	c := r.Counts
	fmt.Fprintf(&b, "\n%s: %d converted (%d to review), %d blocked\n", count(c.Tasks, "task"), c.Converted, c.Review, c.Blocked)
	fmt.Fprintf(&b, "by state: %d asserted, %d computed, %d imperative, %d observe\n", c.Asserted, c.Computed, c.Imperative, c.Observe)
	if len(r.CannotCheck) > 0 {
		fmt.Fprintf(&b, "\n%s cannot take part in check mode (--mode check reports them unchecked):\n", count(len(r.CannotCheck), "converted task"))
		for _, id := range r.CannotCheck {
			fmt.Fprintf(&b, "  %s\n", id)
		}
	}
	return b.String()
}

// count renders n things, noun singular or plural to match.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// orNone renders an empty hosts value readably.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// joinInts renders play numbers as a list.
func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, ", ")
}

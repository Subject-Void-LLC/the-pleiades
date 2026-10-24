// The text view of the crossover report, from the same model -json prints.
package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// modulePrefix is left off package paths in the text view, which every one
// of this module's packages shares.
const modulePrefix = "github.com/Subject-Void-LLC/the-pleiades/"

// short is name without the module's prefix.
func short(name string) string {
	return strings.TrimPrefix(name, modulePrefix)
}

// shortSHA is a commit's first eight characters.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// printReport prints r for a person: the tests, the crossover sections and
// the legs.
func printReport(w io.Writer, r Report) {
	fmt.Fprintf(w, "failures: %d pr-check and %d actions entries", r.Entries[SourcePRCheck], r.Entries[SourceActions])
	if f := r.Filters; f.Since != nil || f.Source != "" || f.Test != "" {
		since := "any time"
		if f.Since != nil {
			since = f.Since.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, " (filtered: since %s, source %q, test %q)", since, f.Source, f.Test)
	}
	fmt.Fprintf(w, "\n\nTests (%d):\n", len(r.Tests))
	byName := map[string]TestSummary{}
	for _, t := range r.Tests {
		byName[testName(t)] = t
		fmt.Fprintf(w, "\n  %s\n", short(testName(t)))
		fmt.Fprintf(w, "      %s | %s\n", counts(t, SourcePRCheck), counts(t, SourceActions))
		var commits []string
		for _, c := range t.Commits {
			commits = append(commits, shortSHA(c))
		}
		fmt.Fprintf(w, "      in %s; on %s; seen %s to %s; at %s\n", strings.Join(t.Legs, ", "), strings.Join(t.OSes, ", "),
			t.FirstSeen.Format("2006-01-02 15:04"), t.LastSeen.Format("2006-01-02 15:04"), strings.Join(commits, " "))
		if len(t.Patterns) > 0 || len(t.Notes) > 0 {
			var ps []string
			for _, p := range t.Patterns {
				ps = append(ps, fmt.Sprintf("%d", p))
			}
			line := "pattern " + strings.Join(ps, ", ")
			if len(t.Notes) > 0 {
				line += ": " + strings.Join(t.Notes, "; ")
			}
			fmt.Fprintf(w, "      %s\n", line)
		}
		if t.LastError != "" {
			fmt.Fprintf(w, "      %s\n", t.LastError)
		}
	}
	section(w, "Actions only (failed in Actions, never in pr-check: a hole in the pre-push gate's scope, tier or environment)", r.ActionsOnly, byName)
	section(w, "pr-check only (failed here, never in Actions: this machine's environment)", r.PRCheckOnly, byName)
	section(w, "Both", r.Both, byName)
	fmt.Fprintf(w, "\nRecurring contention (called contention %d or more times; a test seen here run after run needs a look) (%d):\n", recurringAt, len(r.RecurringContention))
	if len(r.RecurringContention) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, name := range r.RecurringContention {
		fmt.Fprintf(w, "  %s: %d times, in %s\n", short(name), byName[name].Contention, strings.Join(byName[name].Legs, ", "))
	}
	fmt.Fprintf(w, "\nLegs and steps (%d):\n", len(r.Legs))
	if len(r.Legs) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, l := range r.Legs {
		fmt.Fprintf(w, "  %-8s %-24s %-8s %-28s %d", l.Source, l.Leg, l.Verdict, l.Cause, l.Count)
		if l.Cause != l.Example {
			fmt.Fprintf(w, "  e.g. %s", l.Example)
		}
		fmt.Fprintln(w)
	}
}

// section prints one crossover list, each test with its counts.
func section(w io.Writer, title string, names []string, byName map[string]TestSummary) {
	fmt.Fprintf(w, "\n%s (%d):\n", title, len(names))
	if len(names) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, name := range names {
		t := byName[name]
		fmt.Fprintf(w, "  %s  [%s | %s]\n", short(name), counts(t, SourcePRCheck), counts(t, SourceActions))
	}
}

// counts is a test's verdict counts in one source, as "source: verdict N,
// ...", or "source: -" when it never failed there.
func counts(t TestSummary, source string) string {
	c := t.Counts[source]
	if len(c) == 0 {
		return source + ": -"
	}
	var parts []string
	for v, n := range c {
		parts = append(parts, fmt.Sprintf("%s %d", v, n))
	}
	sort.Strings(parts)
	return source + ": " + strings.Join(parts, ", ")
}

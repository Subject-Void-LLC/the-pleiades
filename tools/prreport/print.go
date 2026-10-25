// The text view of a pr-check report, from the same model -json prints:
// one line per pass, leg and step, then the failures that count with their
// own output, the ones tolerated as contention by name, and the skips.
package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// ledgerLimit is how many tests the skip ledger names under each reason.
const ledgerLimit = 8

// placed is a run with where it ran: a Linux pass's name, or a leg's name
// and which of its passes.
type placed struct {
	where string
	run   flakegate.Run
}

// runsOf is every testgate run in r, placed.
func runsOf(r prreport.Report) []placed {
	var out []placed
	for _, p := range r.Passes {
		out = append(out, placed{p.Name, p.Run})
	}
	for _, l := range r.Legs {
		for _, run := range l.Leg.Runs {
			out = append(out, placed{l.Name + " " + passName(run), run})
		}
	}
	return out
}

// passName names a run on a machine by its arguments: the repeats, which
// run each test more than once (testgate -repeat), or the full pass.
func passName(run flakegate.Run) string {
	for _, arg := range strings.Fields(run.Label) {
		if n, ok := strings.CutPrefix(arg, "-count="); ok && n != "1" {
			return "repeat"
		}
	}
	return "full"
}

// printSummary prints r for a person: its verdict, a line for each pass,
// leg and step, the failures that count with their output, the contention
// by name, problems, and how many tests were skipped.
func printSummary(w io.Writer, r prreport.Report, path string) {
	fmt.Fprintf(w, "pr-check%s, %s: %s%s\n  %s\n\n", atCommit(r.Commit), r.Generated.Format("2006-01-02 15:04 MST"), strings.ToUpper(r.Verdict), exitLine(r.ExitCode), path)
	if r.Verdict == prreport.VerdictRunning {
		fmt.Fprintf(w, "  pr-check began this run and has not ended it: this is not a result. Expected: %s\n", strings.Join(r.Expected, ", "))
	}
	if r.Oldest != nil && r.Started != nil && r.Oldest.Before(*r.Started) {
		// Records kept by make pr-check LEGS=...: the receipt's age counts
		// from this time, not from the run's start.
		fmt.Fprintf(w, "  the oldest record began %s, before this run: it was kept from an earlier one\n", r.Oldest.Format("2006-01-02 15:04 MST"))
	}
	for _, p := range r.Passes {
		fmt.Fprintf(w, "  %-11s %-8s %s\n", p.Name, p.Run.Verdict, runLine(p.Run))
	}
	for _, l := range r.Legs {
		fmt.Fprintf(w, "  %-11s %-8s %s\n", l.Name, legVerdict(l.Leg), legLine(l.Leg))
	}
	for _, name := range r.Missing {
		fmt.Fprintf(w, "  %-11s %-8s no record of it\n", name, "missing")
	}
	for _, s := range r.Steps {
		line := fmt.Sprintf("exit %d", s.ExitCode)
		if s.Verdict == flakegate.VerdictNotRun {
			line = s.Reason
		}
		fmt.Fprintf(w, "  %-11s %-8s %s\n", s.Name, s.Verdict, line)
	}
	printFailures(w, r)
	var contention []string
	skipped := 0
	for _, p := range runsOf(r) {
		for _, f := range p.run.Failures {
			if f.Outcome == flakegate.OutcomeContention {
				contention = append(contention, fmt.Sprintf("%s: %s %s", p.where, f.Package, f.Test))
			}
		}
		skipped += len(p.run.Skips)
	}
	if len(contention) > 0 {
		fmt.Fprintf(w, "\nPassed when re-run alone (%d; a test seen here run after run needs a look):\n  %s\n", len(contention), strings.Join(contention, "\n  "))
	}
	for _, p := range r.Problems {
		if p.Code == prreport.ProblemMissing {
			continue // its line is above
		}
		where := ""
		if p.Record != "" {
			where = " " + p.Record + ":"
		}
		fmt.Fprintf(w, "\nProblem (%s)%s %s\n", p.Code, where, p.Detail)
	}
	if skipped > 0 {
		fmt.Fprintf(w, "\n%d test(s) skipped; prreport -skips lists them by reason.\n", skipped)
	}
}

// printFailures prints each failure that counts against its run, with
// where it ran and its own output.
func printFailures(w io.Writer, r prreport.Report) {
	first := true
	for _, p := range runsOf(r) {
		for _, f := range p.run.Failures {
			if f.Outcome == flakegate.OutcomeContention {
				continue
			}
			if first {
				fmt.Fprintln(w, "\nFailures:")
				first = false
			}
			name := f.Test
			if name == "" {
				name = "(the package: " + f.Kind + ")"
			}
			fmt.Fprintf(w, "\n  %s: %s %s [%s]\n", p.where, f.Package, name, f.Outcome)
			if f.Output != "" {
				fmt.Fprintf(w, "      %s\n", strings.ReplaceAll(strings.TrimRight(f.Output, "\n"), "\n", "\n      "))
			}
		}
	}
}

// printSkips prints every skip in r, grouped by reason, each test named
// with where it ran.
func printSkips(w io.Writer, r prreport.Report) {
	var skips []flakegate.Skip
	for _, p := range runsOf(r) {
		for _, s := range p.run.Skips {
			s.Package = p.where + ": " + s.Package
			skips = append(skips, s)
		}
	}
	if ledger := flakegate.SkipLedger(skips, ledgerLimit); ledger != "" {
		fmt.Fprint(w, ledger)
		return
	}
	fmt.Fprintln(w, "No test was skipped.")
}

// runLine is a pass's line: packages, time, failures, contention, skips.
func runLine(run flakegate.Run) string {
	failed, contended := 0, 0
	for _, f := range run.Failures {
		if f.Outcome == flakegate.OutcomeContention {
			contended++
		} else {
			failed++
		}
	}
	line := fmt.Sprintf("%d package(s), %.0fs, %d failed, %d contention, %d skipped", len(run.Packages), run.Seconds, failed, contended, len(run.Skips))
	if run.Error != "" && failed == 0 {
		line += ": " + firstLine(run.Error)
	}
	return line
}

// legVerdict is a leg's verdict, saying a check with no machine set up is
// that rather than not-run.
func legVerdict(l oscheck.LegRecord) string {
	if prreport.NotConfigured(l) {
		return "unset"
	}
	return l.Verdict
}

// legLine is a leg's line: where it ran and how its runs went, or where it
// stopped and why.
func legLine(l oscheck.LegRecord) string {
	switch {
	case prreport.NotConfigured(l):
		return l.OS + ": " + firstLine(l.Error)
	case l.Verdict == flakegate.VerdictNotRun || l.Verdict == flakegate.VerdictError:
		return fmt.Sprintf("%s on %s: stopped at %s: %s", l.OS, l.Target, l.Step, firstLine(l.Error))
	}
	parts := []string{fmt.Sprintf("%s on %s, %d package(s), %.0fs", l.OS, l.Target, len(l.Packages), l.Seconds)}
	for _, run := range l.Runs {
		parts = append(parts, passName(run)+" "+run.Verdict)
	}
	if l.Error != "" {
		parts = append(parts, firstLine(l.Error))
	}
	return strings.Join(parts, "; ")
}

// exitLine is " (exit N)", pr-check's own status, when the report holds
// it, or nothing.
func exitLine(code *int) string {
	if code == nil {
		return ""
	}
	return fmt.Sprintf(" (exit %d)", *code)
}

// atCommit is " at <short commit>", or nothing.
func atCommit(commit string) string {
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if commit == "" {
		return ""
	}
	return " at " + commit
}

// firstLine is text up to its first line break.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

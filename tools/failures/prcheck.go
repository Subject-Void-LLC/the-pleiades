// Entries from a pr-check report (tools/internal/prreport's model, read by
// its own reader): each failure and contention in each pass and each
// machine's runs, and each leg, step or expected part that failed or did
// not run with no test to blame.
package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// runMeta is what every entry from one run shares.
type runMeta struct {
	commit, branch, run string
	seen, now           time.Time
}

// entry starts an entry of kind at leg with what every entry from the run
// shares.
func (m runMeta) entry(kind, leg string) Entry {
	return Entry{Schema: EntrySchema, Source: SourcePRCheck, RecordedAt: m.now, SeenAt: m.seen, Commit: m.commit,
		Branch: m.branch, Run: m.run, Kind: kind, Leg: leg}
}

// runID is a pr-check run's id: when it began and the commit it checked.
func runID(started time.Time, commit string) string {
	return started.UTC().Format(time.RFC3339) + "/" + commit
}

// legEntry is a leg entry with its reason scrubbed.
func (m runMeta) legEntry(leg, verdict, reason string) Entry {
	e := m.entry(KindLeg, leg)
	e.Verdict, e.Reason = verdict, scrub(reason)
	return e
}

// fromReport turns a merged report into entries. A report still running is
// not a result; fromReport is given one only to record it as interrupted.
func fromReport(r prreport.Report, branch string, now time.Time) []Entry {
	seen := r.Generated
	if r.Started != nil {
		seen = *r.Started
	}
	m := runMeta{commit: r.Commit, branch: branch, run: runID(seen, r.Commit), seen: seen.UTC(), now: now.UTC()}
	var out []Entry
	for _, p := range r.Passes {
		failed := m.failures(&out, p.Name, "", p.Run)
		if !failed && (p.Run.Verdict == flakegate.VerdictError || p.Run.Verdict == flakegate.VerdictNotRun || p.Run.Verdict == flakegate.VerdictFail) {
			out = append(out, m.legEntry(p.Name, legVerdict(p.Run.Verdict), p.Run.Error))
		}
	}
	for _, l := range r.Legs {
		failed := false
		for _, run := range l.Leg.Runs {
			if m.failures(&out, l.Name, l.Leg.OS, run) {
				failed = true
			}
		}
		why := untarget(l.Leg.Error, l.Leg.Target)
		switch {
		case prreport.NotConfigured(l.Leg):
			out = append(out, osLeg(m.legEntry(l.Name, VerdictNotRun, why), l.Leg.OS))
		case l.Leg.Verdict == flakegate.VerdictNotRun || l.Leg.Verdict == flakegate.VerdictError:
			out = append(out, osLeg(m.legEntry(l.Name, legVerdict(l.Leg.Verdict), "stopped at "+l.Leg.Step+": "+why), l.Leg.OS))
		case l.Leg.Verdict == flakegate.VerdictFail && !failed:
			out = append(out, osLeg(m.legEntry(l.Name, VerdictFail, why), l.Leg.OS))
		}
	}
	for _, s := range r.Steps {
		switch s.Verdict {
		case flakegate.VerdictFail:
			out = append(out, m.legEntry(s.Name, VerdictFail, "exit "+strconv.Itoa(s.ExitCode)))
		case flakegate.VerdictNotRun:
			out = append(out, m.legEntry(s.Name, VerdictNotRun, s.Reason))
		}
	}
	for _, name := range r.Missing {
		if r.Verdict == prreport.VerdictInterrupted {
			break // what an interrupted run never reached is the interruption's, and its entry says so
		}
		out = append(out, m.legEntry(name, VerdictNotRun, "no record of it: pr-check said it would run it"))
	}
	for _, p := range r.Problems {
		switch p.Code {
		case prreport.ProblemMissing:
			continue // each has its own entry above
		case prreport.ProblemInterrupted:
			out = append(out, m.legEntry("pr-check", VerdictNotRun, "interrupted: "+p.Detail))
		default:
			out = append(out, m.legEntry("pr-check", VerdictFail, p.Code+": "+p.Detail))
		}
	}
	return out
}

// failures appends an entry for each failure in run, placed at leg on os
// (the run's own when os is empty), and reports whether any of them counts
// against the run.
func (m runMeta) failures(out *[]Entry, leg, os string, run flakegate.Run) bool {
	counted := false
	pass := ""
	if os != "" {
		pass = passOf(run.Label) // a machine's leg runs a full pass and a repeat
	} else {
		os = run.OS
	}
	for _, f := range run.Failures {
		e := m.entry(KindTest, leg)
		e.Pass, e.OS, e.Package, e.Test = pass, os, scrub(f.Package), scrub(f.Test)
		e.Verdict = testVerdict(f.Outcome, passOf(run.Label) == "repeat")
		if f.Test == "" {
			e.Reason = packageReason(f.Kind)
		}
		e.Error = firstError(f.Output)
		if e.Verdict != VerdictContention {
			counted = true
		}
		*out = append(*out, e)
	}
	return counted
}

// testVerdict is the ledger's verdict for a failure's outcome in a pass that
// is strict or not.
func testVerdict(outcome string, strict bool) string {
	switch outcome {
	case flakegate.OutcomeContention:
		return VerdictContention
	case flakegate.OutcomeNotRerun:
		return VerdictNotRerun
	case flakegate.OutcomeFailed:
		if strict {
			return VerdictStrict
		}
		return VerdictReal
	}
	return outcome // a value this reader does not know, kept as it is
}

// passOf names a run by its arguments as prreport does: the repeats run each
// test more than once (testgate -repeat, which pr-check runs -strict), and
// anything else is the full pass.
func passOf(label string) string {
	for _, arg := range strings.Fields(label) {
		if n, ok := strings.CutPrefix(arg, "-count="); ok && n != "1" {
			return "repeat"
		}
	}
	return "full"
}

// packageReason says what a package-level failure of kind was, in testgate's
// own words.
func packageReason(kind string) string {
	switch kind {
	case "build":
		return flakegate.FailedBuild.String()
	case "package":
		return flakegate.FailedPackage.String()
	}
	return kind
}

// legVerdict is the ledger's verdict for a pass's or leg's: one that never
// reached its tests did not run, and anything else failed.
func legVerdict(v string) string {
	if v == flakegate.VerdictNotRun {
		return VerdictNotRun
	}
	return VerdictFail
}

// targetMark stands in for a leg's machine, its device or ssh name.
const targetMark = "<target>"

// untarget replaces a leg's machine name in text with a placeholder, since a
// ledger records which OS a leg ran on and never which machine.
func untarget(text, target string) string {
	if target == "" {
		return text
	}
	return strings.ReplaceAll(text, target, targetMark)
}

// osLeg sets e's OS.
func osLeg(e Entry, os string) Entry {
	e.OS = os
	return e
}

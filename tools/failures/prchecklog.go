// Entries from a saved pr-check log, for a run whose records were overwritten
// by a later one: the prreport summary at its end (the line for each pass,
// leg and step, the Failures block and the contention block) and the
// receipt's refusal after it.
package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	// summaryRE is the summary's first line: the short commit, when it was
	// written, and the verdict.
	summaryRE = regexp.MustCompile(`^pr-check(?: at ([0-9a-f]+))?, (\d{4}-\d{2}-\d{2} \d{2}:\d{2} [A-Z]+): ([A-Z-]+)`)
	// tableRE is a pass, leg or step line in the summary.
	tableRE = regexp.MustCompile(`^  (\S+)\s+(pass|fail|not-run|error|unset|missing)\s+(.*)$`)
	// failureRE is a failure line: where, package, test (or the package's
	// kind) and outcome.
	failureRE = regexp.MustCompile(`^  ([a-z]+(?: [a-z]+)?): (\S+) (.+) \[([a-z-]+)\]$`)
	// contentionRE is a contention line: where, package and test.
	contentionRE = regexp.MustCompile(`^  ([a-z]+(?: [a-z]+)?): (\S+) (\S+)$`)
	// packageTestRE is the test of a package-level failure, by its kind.
	packageTestRE = regexp.MustCompile(`^\(the package: ([a-z]+)\)$`)
	// problemRE is a problem line: its code and, after its record, its
	// detail.
	problemRE = regexp.MustCompile(`^Problem \(([a-z-]+)\)(?: \S+:)? (.*)$`)
	// legOSRE is the start of a machine leg's line: its OS and machine.
	legOSRE = regexp.MustCompile(`^(\S+) on ([^,:]+)`)
)

// prcheckLog is what a saved log's summary says.
type prcheckLog struct {
	short   string
	written time.Time
	verdict string
	entries []Entry
}

// legOS is each machine leg's OS when its line does not say.
var legOS = map[string]string{"mac": "macOS", "bsd": "FreeBSD", "win": "Windows"}

// linuxPasses are the passes pr-check runs on this machine.
var linuxPasses = map[string]bool{"fast": true, "repeat": true, "containers": true}

// parsePRCheckLog reads the last prreport summary in text into entries
// without a commit, run or times, which the caller sets. A log with no
// summary is an error: pr-check stopped before its merge printed one.
func parsePRCheckLog(text string) (prcheckLog, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if summaryRE.MatchString(l) {
			start = i
		}
	}
	if start < 0 {
		return prcheckLog{}, fmt.Errorf("no prreport summary in the log: the run stopped before its merge printed one")
	}
	head := summaryRE.FindStringSubmatch(lines[start])
	out := prcheckLog{short: head[1], verdict: strings.ToLower(head[3])}
	if t, err := time.Parse("2006-01-02 15:04 MST", head[2]); err == nil {
		out.written = t.UTC()
	}
	m := runMeta{}
	oses, targets := map[string]string{}, map[string]string{}
	counted := map[string]bool{}
	type legLine struct{ name, verdict, rest string }
	var table []legLine
	block := ""
	var last *Entry
	var output []string
	flush := func() {
		if last != nil {
			last.Error = firstError(strings.Join(output, "\n"))
		}
		last, output = nil, nil
	}
	for _, l := range lines[start+1:] {
		switch {
		case strings.HasPrefix(l, "Failures:"):
			block = "failures"
			continue
		case strings.HasPrefix(l, "Passed when re-run alone"):
			flush()
			block = "contention"
			continue
		case problemRE.MatchString(l):
			flush()
			if p := problemRE.FindStringSubmatch(l); p[1] == "interrupted" {
				out.entries = append(out.entries, m.legEntry("pr-check", VerdictNotRun, "interrupted: "+p[2]))
			} else if p[1] != "missing" { // a missing part has its own line in the table
				out.entries = append(out.entries, m.legEntry("pr-check", VerdictFail, p[1]+": "+p[2]))
			}
			block = "problems"
			continue
		case strings.HasPrefix(l, "gatereceipt write: "):
			flush()
			out.entries = append(out.entries, m.legEntry("receipt", VerdictFail, strings.TrimPrefix(l, "gatereceipt write: ")))
			block = "done"
			continue
		}
		switch block {
		case "":
			if t := tableRE.FindStringSubmatch(l); t != nil {
				table = append(table, legLine{t[1], t[2], t[3]})
				if o := legOSRE.FindStringSubmatch(t[3]); o != nil && legOS[t[1]] != "" {
					oses[t[1]], targets[t[1]] = o[1], o[2]
				}
			}
		case "failures":
			if strings.HasPrefix(l, "      ") && last != nil {
				output = append(output, l)
				continue
			}
			f := failureRE.FindStringSubmatch(l)
			if f == nil {
				continue
			}
			flush()
			e := m.logTest(f[1], f[2], f[3], oses)
			strict := strings.HasSuffix(f[1], "repeat")
			e.Verdict = testVerdict(f[4], strict)
			if e.Verdict != VerdictContention {
				counted[e.Leg] = true
			}
			out.entries = append(out.entries, e)
			last = &out.entries[len(out.entries)-1]
		case "contention":
			if f := contentionRE.FindStringSubmatch(l); f != nil {
				e := m.logTest(f[1], f[2], f[3], oses)
				e.Verdict = VerdictContention
				out.entries = append(out.entries, e)
			}
		}
	}
	flush()
	// The summary names a contention without its output; testgate's own
	// account of the pass, above the summary, has it.
	said := map[string]string{}
	for _, f := range parseJobLog(strings.Join(lines[:start], "\n")).failures {
		if f.err != "" {
			said[f.pkg+" "+f.test] = f.err
		}
	}
	for i := range out.entries {
		if e := &out.entries[i]; e.Kind == KindTest && e.Error == "" {
			e.Error = said[e.Package+" "+e.Test]
		}
	}
	for _, t := range table {
		if t.verdict == "missing" && out.verdict == "interrupted" {
			continue // what an interrupted run never reached is the interruption's
		}
		if e, ok := m.logLeg(t.name, t.verdict, t.rest, oses[t.name], targets[t.name], counted[t.name]); ok {
			out.entries = append(out.entries, e)
		}
	}
	return out, nil
}

// logTest is a test entry from a failure or contention line: where it ran
// (a pass, or a machine leg and which of its passes), its package and its
// test or the package's kind.
func (m runMeta) logTest(where, pkg, test string, oses map[string]string) Entry {
	leg, pass, _ := strings.Cut(where, " ")
	e := m.entry(KindTest, leg)
	e.Pass, e.Package = pass, scrub(pkg)
	switch {
	case linuxPasses[leg]:
		e.OS = "linux"
	case oses[leg] != "":
		e.OS = oses[leg]
	default:
		e.OS = legOS[leg]
	}
	if k := packageTestRE.FindStringSubmatch(test); k != nil {
		e.Reason = packageReason(k[1])
	} else {
		e.Test = scrub(test)
	}
	return e
}

// logLeg is a leg entry from a summary line, when the line is a leg or step
// that failed or did not run with no failure line to blame.
func (m runMeta) logLeg(name, verdict, rest, os, target string, counted bool) (Entry, bool) {
	rest = untarget(rest, target)
	if i := strings.Index(rest, "stopped at "); i >= 0 {
		rest = rest[i:]
	}
	var e Entry
	switch verdict {
	case "pass":
		return Entry{}, false
	case "unset":
		_, why, _ := strings.Cut(rest, ": ")
		e = m.legEntry(name, VerdictNotRun, why)
	case "missing":
		e = m.legEntry(name, VerdictNotRun, "no record of it: pr-check said it would run it")
	case "not-run":
		e = m.legEntry(name, VerdictNotRun, rest)
	default: // fail, error
		if counted {
			return Entry{}, false // its failures have their own entries
		}
		e = m.legEntry(name, VerdictFail, rest)
	}
	if linuxPasses[name] {
		e.OS = "linux"
	} else if os != "" {
		e.OS = os
	} else {
		e.OS = legOS[name]
	}
	return e, true
}

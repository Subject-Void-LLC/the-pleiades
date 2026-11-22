// Command gosec-check runs gosec and fails on a finding not already
// recorded, with a written reason, in gosec-waivers.json at the repo
// root, and on any file gosec could not type-check and so scanned nothing
// in, unless the same file names it as a skip with a reason (skips.go). This is the per-finding waiver mechanism the Phase 0 CI harness
// item's pre-existing-findings policy calls for
// (.SPECIFICATION/IMPLEMENTATION.md): every waived finding is named
// individually by file, line, and rule, never suppressed by rule ID or
// directory as a whole, so a genuinely new finding anywhere in the
// module still fails the build.
//
// Usage: go run ./tools/gosec-check [-timeout 30m] [-report file.json]
//
// gosec itself must already be on PATH (AGENTS.md's gopls/gosec/
// govulncheck setup section). A package gosec would load in pieces from a
// checkout with a long path is scanned from a short path (scan.go), and an
// interrupt or the time limit stops gosec and every process it started
// (process.go).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// gosecIssue is the subset of one gosec JSON finding this tool reads.
type gosecIssue struct {
	File   string `json:"file"`
	Line   string `json:"line"`
	RuleID string `json:"rule_id"`
	Detail string `json:"details"`
}

// gosecReport is the subset of gosec's JSON report this tool reads: the
// findings, the files it could not type-check, by path, and its counts.
type gosecReport struct {
	Issues       []gosecIssue            `json:"Issues"`
	GolangErrors map[string][]gosecError `json:"Golang errors"`
	Stats        gosecStats              `json:"Stats"`
}

// gosecStats is gosec's count of what it scanned and found.
type gosecStats struct {
	Files int `json:"files"`
	Lines int `json:"lines"`
	Nosec int `json:"nosec"`
	Found int `json:"found"`
}

// waiver is one accepted, individually-justified finding.
type waiver struct {
	File   string `json:"file"`
	Line   string `json:"line"`
	RuleID string `json:"rule_id"`
	Reason string `json:"reason"`
}

// waiverFile is gosec-waivers.json: the waived findings, and the files
// accepted as never type-checking.
type waiverFile struct {
	Waivers []waiver     `json:"waivers"`
	Skips   []skipWaiver `json:"skips"`
}

type waiverKey struct {
	file   string
	line   string
	ruleID string
}

// main parses the flags and runs the check.
func main() {
	timeout := flag.Duration("timeout", defaultTimeout, "stop gosec, and what it started, after this long (0 for no limit)")
	reportPath := flag.String("report", "", "also write the joined gosec report, as JSON, to this file")
	flag.Parse()
	root, err := os.Getwd()
	if err == nil {
		err = run(root, *timeout, *reportPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gosec-check:", err)
		os.Exit(1)
	}
}

// defaultTimeout is how long a scan may run before it is stopped: about five
// times the slowest run measured here (127 s, from a checkout whose build
// cache was cold; warm runs take 17 to 60 s), so a cold CI runner has room,
// and far inside the ci job's own 45 minutes, which runs every static check.
// It is not measured on a CI runner. A gate's gosec stage once hung for 27
// minutes (FAILURE_PATTERNS 1337).
const defaultTimeout = 10 * time.Minute

// run loads the waivers of the module at root, the working directory, runs
// gosec over it, stopping it on an interrupt or after timeout, and judges
// what it reported (judge).
func run(root string, timeout time.Duration, reportPath string) error {
	waivers, skips, err := loadWaivers(filepath.Join(root, "gosec-waivers.json"))
	if err != nil {
		return fmt.Errorf("loading gosec-waivers.json: %w", err)
	}

	stop, release := interruptions()
	defer release()
	s := scanner{gosec: "gosec", stop: stop, log: os.Stderr}
	if timeout > 0 {
		s.deadline = time.Now().Add(timeout)
	}
	report, err := s.scan(root)
	if err != nil {
		return err
	}
	if reportPath != "" {
		if err := writeReport(reportPath, report); err != nil {
			return err
		}
	}
	return judge(report, waivers, skips, root, os.Stdout, os.Stderr)
}

// interruptions returns a channel closed at the first interrupt, SIGTERM or
// SIGHUP, so the scan stops gosec and what it started rather than gosec-check
// ending and leaving them running, and a function that stops listening.
func interruptions() (<-chan struct{}, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	stop := make(chan struct{})
	go func() {
		if _, ok := <-signals; ok {
			close(stop)
		}
	}()
	return stop, func() { signal.Stop(signals) }
}

// writeReport writes report to path as indented JSON.
func writeReport(path string, report *gosecReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing the report: %w", err)
	}
	return nil
}

// judge holds report, gosec's decoded output for the module at repoRoot, to
// the waivers and the skips: a finding no waiver names fails, a waiver no
// finding matches is stale and fails, and a file gosec could not type-check
// fails unless a skip names it (checkSkips). It writes the pass to out and
// every failure to errOut, and returns an error when any failed.
func judge(report *gosecReport, waivers map[waiverKey]waiver, skips []skipWaiver, repoRoot string, out, errOut io.Writer) error {
	seen := make(map[waiverKey]bool, len(waivers))
	var unwaived []gosecIssue
	for _, issue := range report.Issues {
		rel, err := filepath.Rel(repoRoot, issue.File)
		if err != nil {
			rel = issue.File
		}
		key := waiverKey{file: rel, line: issue.Line, ruleID: issue.RuleID}
		if _, ok := waivers[key]; ok {
			seen[key] = true
			continue
		}
		unwaived = append(unwaived, issue)
	}

	// A waiver entry that no longer matches any real finding is exactly
	// as dangerous as a blanket suppression would be, just delayed: it
	// silently stops meaning anything the moment the code moves. Fail
	// loudly instead of letting it rot.
	var stale []waiverKey
	for key := range waivers {
		if !seen[key] {
			stale = append(stale, key)
		}
	}

	// Before the findings' verdict: a scan that skipped files has not
	// read them, so "all waived" would be a statement about fewer files
	// than gosec was asked to scan. It covers only what gosec names: a
	// package gosec cannot name at all, and a file excluded by GOOS or build
	// tags, are outside it.
	skipped := checkSkips(report.GolangErrors, skips, repoRoot)
	if skipped.failed() {
		fmt.Fprint(errOut, skipped.report(20))
	}

	if len(unwaived) == 0 && len(stale) == 0 && !skipped.failed() {
		fmt.Fprintf(out, "gosec-check: %d finding(s) in %d file(s), all individually waived in gosec-waivers.json\n", len(report.Issues), report.Stats.Files)
		return nil
	}

	if len(unwaived) > 0 {
		fmt.Fprintf(errOut, "gosec-check: %d finding(s) not in gosec-waivers.json:\n\n", len(unwaived))
		for _, issue := range unwaived {
			fmt.Fprintf(errOut, "  %s:%s [%s] %s\n", issue.File, issue.Line, issue.RuleID, issue.Detail)
		}
		fmt.Fprintln(errOut, "\nFix it, or add a gosec-waivers.json entry with a written reason (never a blanket rule/directory exclusion).")
	}
	if len(stale) > 0 {
		sort.Slice(stale, func(i, j int) bool {
			if stale[i].file != stale[j].file {
				return stale[i].file < stale[j].file
			}
			return stale[i].line < stale[j].line
		})
		fmt.Fprintf(errOut, "\ngosec-check: %d stale waiver(s) in gosec-waivers.json no longer match any finding (remove or update them):\n\n", len(stale))
		for _, key := range stale {
			fmt.Fprintf(errOut, "  %s:%s [%s]\n", key.file, key.line, key.ruleID)
		}
	}
	return fmt.Errorf("gosec check failed")
}

// loadWaivers reads gosec-waivers.json: the waived findings by key, and the
// skip waivers. A skip waiver with no reason is refused, as an unexplained
// exemption would be.
func loadWaivers(path string) (map[waiverKey]waiver, []skipWaiver, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed repo-relative path, not user input
	if err != nil {
		return nil, nil, err
	}
	var wf waiverFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, nil, err
	}
	out := make(map[waiverKey]waiver, len(wf.Waivers))
	for _, w := range wf.Waivers {
		out[waiverKey{file: w.File, line: w.Line, ruleID: w.RuleID}] = w
	}
	for _, s := range wf.Skips {
		if s.File == "" || s.Reason == "" {
			return nil, nil, fmt.Errorf("a skip waiver must name a file and give a reason: %+v", s)
		}
	}
	return out, wf.Skips, nil
}

// decodeReport reads gosec's JSON report.
func decodeReport(raw []byte) (*gosecReport, error) {
	var report gosecReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

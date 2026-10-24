// Command failures keeps two failure ledgers, one for local make pr-check
// runs and one for GitHub Actions runs, and reports their crossover: which
// tests fail only in Actions (a hole in the pre-push gate), only here (this
// machine's environment), or in both, which keep losing races, and which
// legs and steps fail with no test to blame. It finds an antipattern before
// it costs a pull request a red run.
//
// Usage:
//
//	go run ./tools/failures record-prcheck [-report FILE] [-interrupted] [-branch B]
//	go run ./tools/failures record-prcheck -log FILE [-commit SHA] [-at RFC3339] [-branch B]
//	go run ./tools/failures record-actions (-run RUN_ID | -pr N) [-failed-only]
//	go run ./tools/failures watch-pr -pr N [-every 2m]
//	go run ./tools/failures report [-json] [-since DATE] [-source S] [-test REGEX]
//	go run ./tools/failures annotate -test PKG.Test [-commit SHA] -pattern N [-note TEXT]
//
// Each takes -dir, the ledgers' directory, which defaults to one that git
// ignores: make pr-check records every run there, and a tracked file would
// dirty the tree its receipt needs clean. An entry stores the OS and leg or
// job, never a host or address, and its free text is masked (scrub). Each
// ledger is one JSON entry per line (Entry, schema EntrySchema); a run
// recorded twice adds nothing. Beside them, record-prcheck keeps what each
// run tested (Scope, in pr-check-scope.jsonl), so the report can name a test
// that failed only in Actions in a package pr-check did not test at that
// commit. watch-pr follows a pull request's checks until none is pending,
// then records its run and prints the report's Actions-only and recurring
// sections; it exits 1 when a check failed.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// defaultReport is where make pr-check leaves its merged report.
const defaultReport = ".coverage/pr/records/" + prreport.ReportFile

func main() {
	learnSecrets()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, ghCLI{}))
}

// run is main with its arguments, output and GitHub given, so tests drive
// it.
func run(args []string, stdout, stderr io.Writer, gh github) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "failures: name a command: record-prcheck, record-actions, watch-pr, report or annotate")
		return 2
	}
	var err error
	switch args[0] {
	case "record-prcheck":
		err = runRecordPRCheck(args[1:], stdout, stderr)
	case "record-actions":
		err = runRecordActions(args[1:], stdout, stderr, gh)
	case "watch-pr":
		err = runWatchPR(args[1:], stdout, stderr, gh)
	case "report":
		err = runReport(args[1:], stdout, stderr)
	case "annotate":
		err = runAnnotate(args[1:], stdout, stderr)
	default:
		err = usageError{fmt.Sprintf("unknown command %q: record-prcheck, record-actions, watch-pr, report or annotate", args[0])}
	}
	var usage usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &usage), errors.Is(err, flag.ErrHelp):
		fmt.Fprintf(stderr, "failures %s: %v\n", args[0], err)
		return 2
	}
	fmt.Fprintf(stderr, "failures %s: %v\n", args[0], err)
	return 1
}

// usageError is a command line this tool cannot read.
type usageError struct{ msg string }

// Error is the message.
func (u usageError) Error() string { return u.msg }

// flags parses args into fs, refusing anything left over.
func flags(fs *flag.FlagSet, args []string, stderr io.Writer) error {
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() > 0 {
		return usageError{fmt.Sprintf("unexpected argument %q: every input is a flag", fs.Arg(0))}
	}
	return nil
}

// now is the clock, a variable so a test sets it.
var now = time.Now

// runRecordPRCheck records a pr-check run from its report, or from a saved
// log's summary.
func runRecordPRCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("record-prcheck", flag.ContinueOnError)
	dir := fs.String("dir", defaultDir, "the ledgers' directory")
	report := fs.String("report", defaultReport, "the merged pr-check report")
	logFile := fs.String("log", "", "a saved pr-check log to read in place of the report")
	commit := fs.String("commit", "", "with -log, the commit the run checked (default: the summary's)")
	at := fs.String("at", "", "with -log, when the run began, RFC3339 (default: when its summary was written)")
	branch := fs.String("branch", "", "the branch the run was on (default: the current one)")
	interrupted := fs.Bool("interrupted", false, "record a report still saying running as an interrupted run")
	if err := flags(fs, args, stderr); err != nil {
		return err
	}
	if *branch == "" {
		*branch = currentBranch()
	}
	if *logFile != "" {
		entries, err := entriesFromLog(*logFile, *commit, *at, *branch)
		if err != nil {
			return err
		}
		// A log's summary names no packages, so it records no scope.
		return appendAndSay(stdout, ledgerFile(*dir, SourcePRCheck), entries)
	}
	r, err := prreport.ReadReport(*report)
	if err != nil {
		return err
	}
	if r.Verdict == prreport.VerdictRunning {
		if !*interrupted {
			return fmt.Errorf("the report for %s still says running, which is not a result: wait for the run, or pass -interrupted for one that stopped before its merge", shortSHA(r.Commit))
		}
		if r, err = prreport.Merge(filepath.Dir(*report), prreport.MergeOptions{Commit: r.Commit, Interrupted: true, Now: now()}); err != nil {
			return err
		}
	}
	if err := appendAndSay(stdout, ledgerFile(*dir, SourcePRCheck), fromReport(r, *branch, now())); err != nil {
		return err
	}
	// What the run tested goes beside the ledger, passing run or not, so
	// the report can tell a test pr-check never ran from one it ran and
	// passed.
	if s, ok := scopeOf(r, now()); ok {
		added, err := appendScope(scopeFile(*dir), s)
		if err != nil {
			return err
		}
		if added {
			fmt.Fprintf(stdout, "failures: the run's scope (%d packages) recorded in %s\n", len(s.Packages), scopeFile(*dir))
		}
	}
	return nil
}

// entriesFromLog reads a saved log's summary into entries for its run.
func entriesFromLog(path, commit, at, branch string) ([]Entry, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the log this tool was pointed at
	if err != nil {
		return nil, err
	}
	l, err := parsePRCheckLog(string(data))
	if err != nil {
		return nil, err
	}
	if commit == "" {
		commit = l.short
	}
	full, err := resolveCommit(commit)
	if err != nil {
		return nil, err
	}
	seen := l.written
	if at != "" {
		if seen, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, usageError{"-at: " + err.Error()}
		}
	}
	for i := range l.entries {
		e := &l.entries[i]
		e.Commit, e.Branch, e.SeenAt, e.RecordedAt, e.Run = full, branch, seen.UTC(), now().UTC(), runID(seen, full)
	}
	return l.entries, nil
}

// runRecordActions records a workflow run's jobs.
func runRecordActions(args []string, stdout, stderr io.Writer, gh github) error {
	fs := flag.NewFlagSet("record-actions", flag.ContinueOnError)
	dir := fs.String("dir", defaultDir, "the ledgers' directory")
	runID := fs.Int64("run", 0, "the workflow run's id")
	pr := fs.Int("pr", 0, "a pull request, whose latest completed run is recorded")
	failedOnly := fs.Bool("failed-only", false, "read only the jobs that did not pass, missing contention in the ones that did")
	if err := flags(fs, args, stderr); err != nil {
		return err
	}
	if (*runID == 0) == (*pr == 0) {
		return usageError{"pass one of -run and -pr"}
	}
	if *pr != 0 {
		id, err := latestRunForPR(gh, *pr)
		if err != nil {
			return err
		}
		*runID = id
	}
	entries, err := fromRun(gh, *runID, *failedOnly, now())
	if err != nil {
		return err
	}
	return appendAndSay(stdout, ledgerFile(*dir, SourceActions), entries)
}

// appendAndSay appends entries to the ledger at path and says how many.
func appendAndSay(stdout io.Writer, path string, entries []Entry) error {
	n, err := appendLedger(path, entries)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "failures: %d of %d entries new in %s\n", n, len(entries), path)
	return nil
}

// runReport prints the crossover report.
func runReport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	dir := fs.String("dir", defaultDir, "the ledgers' directory")
	asJSON := fs.Bool("json", false, "print the report's model as JSON")
	since := fs.String("since", "", "leave out what was seen before this date (2006-01-02) or time (RFC3339)")
	source := fs.String("source", "", "only pr-check or only actions")
	test := fs.String("test", "", "only tests whose package.Test matches this regular expression")
	if err := flags(fs, args, stderr); err != nil {
		return err
	}
	f := Filters{Source: *source, Test: *test}
	if *source != "" && !known(*source, KnownSources) {
		return usageError{fmt.Sprintf("-source is pr-check or actions, not %q", *source)}
	}
	if *since != "" {
		t, err := time.Parse(time.RFC3339, *since)
		if err != nil {
			if t, err = time.Parse("2006-01-02", *since); err != nil {
				return usageError{"-since is a date (2006-01-02) or an RFC3339 time"}
			}
		}
		f.Since = &t
	}
	entries, err := readAll(*dir)
	if err != nil {
		return err
	}
	scopes, err := readScopes(scopeFile(*dir))
	if err != nil {
		return err
	}
	r, err := buildReport(entries, scopes, f, now())
	if err != nil {
		return usageError{"-test: " + err.Error()}
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	printReport(stdout, r)
	return nil
}

// runAnnotate sets a pattern and note on matching entries.
func runAnnotate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("annotate", flag.ContinueOnError)
	dir := fs.String("dir", defaultDir, "the ledgers' directory")
	test := fs.String("test", "", "the test, as PKG.Test (a tail of the package's path will do), or a package for its package-level failures")
	commit := fs.String("commit", "", "only entries at this commit, or a prefix of it")
	pattern := fs.Int("pattern", 0, "the FAILURE_PATTERNS number")
	note := fs.String("note", "", "a note to set")
	if err := flags(fs, args, stderr); err != nil {
		return err
	}
	if *test == "" {
		return usageError{"-test is required"}
	}
	n, err := annotate(*dir, *test, *commit, *pattern, *note)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "failures: annotated %d entries\n", n)
	return nil
}

// hexRE is a commit SHA or a prefix of one.
var hexRE = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// resolveCommit is the full SHA a commit or its prefix names; a variable so
// a test resolves its own.
var resolveCommit = func(commit string) (string, error) {
	if commit == "" {
		return "", usageError{"no commit: pass -commit, since the log's summary names none"}
	}
	if !hexRE.MatchString(commit) {
		return "", usageError{fmt.Sprintf("%q is not a commit SHA or a prefix of one", commit)}
	}
	out, err := exec.Command("git", "rev-parse", "--verify", "--quiet", commit+"^{commit}").Output() // #nosec G204 -- git with a commit name, never a shell
	if err != nil {
		return "", fmt.Errorf("%s names no commit in this repository", commit)
	}
	return strings.TrimSpace(string(out)), nil
}

// currentBranch is the branch HEAD is on, or "" when it is detached; a
// variable so a test names its own.
var currentBranch = func() string {
	out, err := exec.Command("git", "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

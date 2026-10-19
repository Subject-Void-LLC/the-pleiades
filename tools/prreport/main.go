// Command prreport reads a pr-check run as one document rather than as the
// text of several logs. Every testgate pass pr-check runs here, each OS
// check's leg (with the records of the passes on its machine) and each
// step it runs besides leave a record in a directory; prreport merges them
// into report.json there, with one verdict, and prints the run for a
// person: a line for each pass, leg and step, then each failure that counts
// with its own output. The JSON and the text are the same model.
//
// It decides nothing: pr-check's exit status is the gate, unchanged, and a
// report whose verdict disagrees with it is a defect in one of the two. The
// report is a versioned contract for tools (tools/internal/prreport, and
// its published schema docs/reference/schemas/pr-check-report.json).
//
// Usage:
//
//	go run ./tools/prreport [-dir D] [-json] [-failures] [-skips] [-leg NAME]
//	go run ./tools/prreport begin [-dir D] [-commit SHA] [-expect NAME,...]
//	go run ./tools/prreport merge [-dir D] [-commit SHA] [-exit CODE] [-interrupted]
//	go run ./tools/prreport step -dir D -name NAME (-exit CODE | -not-run REASON)
//
// The directory defaults to .coverage/pr/records, where make pr-check
// writes them. begin writes a running report naming what the run expects,
// so an expected part with no record is missing from the merge rather than
// unnoticed, and a run that never merges still says it has not ended.
// merge's -exit is pr-check's own status, so the report never passes a run
// the gate failed, and -interrupted marks a run that stopped early. -leg
// names a pass (fast, repeat, containers), a leg (mac, bsd, win) or a step;
// with -json it prints that one entry. A report for a commit other than
// HEAD is said to be one.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// defaultDir is where make pr-check writes its records.
const defaultDir = ".coverage/pr/records"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main with its arguments and output given, so tests drive it.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "begin":
			return runBegin(args[1:], stderr)
		case "merge":
			return runMerge(args[1:], stdout, stderr)
		case "step":
			return runStep(args[1:], stderr)
		}
	}
	return runShow(args, stdout, stderr)
}

// runBegin writes a running report naming what the run expects.
func runBegin(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("prreport begin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", defaultDir, "the records directory")
	commit := fs.String("commit", "", "the commit the run checks")
	expect := fs.String("expect", "", "the passes, legs and steps the run will record, comma separated")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := noArguments(fs); err != nil {
		fmt.Fprintf(stderr, "prreport begin: %v\n", err)
		return 2
	}
	var expected []string
	for _, name := range strings.Split(*expect, ",") {
		if name = strings.TrimSpace(name); name != "" {
			expected = append(expected, name)
		}
	}
	if _, err := prreport.Begin(*dir, *commit, expected, time.Now()); err != nil {
		fmt.Fprintf(stderr, "prreport: %v\n", err)
		return 2
	}
	return 0
}

// runMerge merges the records in -dir into report.json there and prints
// the run.
func runMerge(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prreport merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", defaultDir, "the records directory")
	commit := fs.String("commit", "", "the commit the run checked")
	interrupted := fs.Bool("interrupted", false, "the run stopped before its end")
	var exitCode *int
	fs.Func("exit", "pr-check's own exit status", func(v string) error {
		n, err := strconv.Atoi(v)
		exitCode = &n
		return err
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := noArguments(fs); err != nil {
		fmt.Fprintf(stderr, "prreport merge: %v\n", err)
		return 2
	}
	r, err := prreport.Merge(*dir, prreport.MergeOptions{Commit: *commit, ExitCode: exitCode, Interrupted: *interrupted, Now: time.Now()})
	if err != nil {
		fmt.Fprintf(stderr, "prreport: %v\n", err)
		return 2
	}
	path := filepath.Join(*dir, prreport.ReportFile)
	if err := prreport.WriteJSON(path, r); err != nil {
		fmt.Fprintf(stderr, "prreport: %v\n", err)
		return 2
	}
	printSummary(stdout, r, path)
	return 0
}

// runStep records one step's exit code.
func runStep(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("prreport step", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", defaultDir, "the records directory")
	name := fs.String("name", "", "the step's name: letters, digits and -")
	exit := fs.Int("exit", 0, "the step's exit code")
	notRun := fs.String("not-run", "", "why the step did not run, in place of -exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := noArguments(fs); err != nil {
		fmt.Fprintf(stderr, "prreport step: %v\n", err)
		return 2
	}
	if *notRun != "" && flagSet(fs, "exit") == 1 {
		fmt.Fprintln(stderr, "prreport step: pass -exit or -not-run, not both: a step that did not run has no exit code")
		return 2
	}
	if err := prreport.WriteStep(*dir, *name, *exit, *notRun); err != nil {
		fmt.Fprintf(stderr, "prreport: %v\n", err)
		return 2
	}
	return 0
}

// runShow prints the merged report, or a part of it.
func runShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prreport", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", defaultDir, "the records directory")
	asJSON := fs.Bool("json", false, "print the report, or with -leg that entry, as JSON")
	failures := fs.Bool("failures", false, "print only the failures that count, with their output")
	skips := fs.Bool("skips", false, "print every skip, grouped by reason")
	leg := fs.String("leg", "", "print only this pass, leg or step")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := noArguments(fs); err != nil {
		fmt.Fprintf(stderr, "prreport: %v\n", err)
		return 2
	}
	if views := flagSet(fs, "json") + flagSet(fs, "failures") + flagSet(fs, "skips"); views > 1 {
		fmt.Fprintln(stderr, "prreport: pass one of -json, -failures and -skips: each is a whole view of the report")
		return 2
	}
	path := filepath.Join(*dir, prreport.ReportFile)
	r, err := prreport.ReadReport(path)
	if err != nil {
		fmt.Fprintf(stderr, "prreport: %v\n", err)
		return 2
	}
	if head := headCommit(); r.Commit != "" && head != "" && head != r.Commit {
		fmt.Fprintf(stderr, "prreport: this report is for %s, and HEAD is %s: run make pr-check for this commit\n", r.Commit, head)
	}
	if *leg != "" {
		if r, err = only(r, *leg); err != nil {
			fmt.Fprintf(stderr, "prreport: %v\n", err)
			return 2
		}
	}
	switch {
	case *asJSON:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			fmt.Fprintf(stderr, "prreport: %v\n", err)
			return 2
		}
	case *failures:
		printFailures(stdout, r)
	case *skips:
		printSkips(stdout, r)
	default:
		printSummary(stdout, r, path)
	}
	return 0
}

// noArguments refuses arguments left after fs's flags, which no command
// takes: a word meant as a flag would otherwise be dropped unread.
func noArguments(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q: every input is a flag", fs.Arg(0))
	}
	return nil
}

// flagSet is 1 when the flag called name was given on the command line,
// whatever its value, and 0 otherwise.
func flagSet(fs *flag.FlagSet, name string) int {
	set := 0
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = 1
		}
	})
	return set
}

// headCommit is the commit HEAD names, or "" outside a repository; a
// variable so a test names its own.
var headCommit = func() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// only returns r holding just the pass, leg or step called name, with its
// verdict as the report's and the problems about its record.
func only(r prreport.Report, name string) (prreport.Report, error) {
	out := prreport.Report{Schema: r.Schema, Kind: r.Kind, Commit: r.Commit, Started: r.Started, Generated: r.Generated}
	for _, p := range r.Problems {
		if p.Record == name || p.Record == name+".json" {
			out.Problems = append(out.Problems, p)
		}
	}
	for _, p := range r.Passes {
		if p.Name == name {
			out.Passes, out.Verdict = []prreport.Pass{p}, p.Run.Verdict
			return out, nil
		}
	}
	for _, l := range r.Legs {
		if l.Name == name {
			out.Legs, out.Verdict = []prreport.Leg{l}, legVerdict(l.Leg)
			return out, nil
		}
	}
	for _, s := range r.Steps {
		if s.Name == name {
			out.Steps, out.Verdict = []prreport.Step{s}, s.Verdict
			return out, nil
		}
	}
	if slices.Contains(r.Missing, name) {
		out.Missing, out.Verdict = []string{name}, prreport.VerdictIncomplete
		return out, nil
	}
	return prreport.Report{}, fmt.Errorf("the report has no pass, leg or step called %q", name)
}

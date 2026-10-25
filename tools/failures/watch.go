// watch-pr: following a pull request's checks until none is pending,
// printing each as it finishes, then recording its Actions run and printing
// what the ledgers say about it, so a red check is matched against the
// failures seen before without being asked for.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The buckets gh pr checks sorts a check into. gh 2.4.0 prints these four;
// a bucket this reader does not know counts as finished and not passed, so
// a newer gh can never turn a failure into a pass here.
const (
	bucketPass     = "pass"
	bucketFail     = "fail"
	bucketPending  = "pending"
	bucketSkipping = "skipping"
)

// maxMisses is how many times in a row gh may fail to answer, or answer
// with no check at all (a pull request whose workflow has not started
// yet), before watch-pr stops waiting.
const maxMisses = 5

// sleep waits between polls; a variable so a test does not wait.
var sleep = time.Sleep

// check is one line of gh pr checks: a check's name, its bucket, how long
// it took and its link.
type check struct {
	name, bucket, elapsed, link string
}

// parseChecks reads gh pr checks' tab-separated lines. A line with fewer
// than a name and a bucket is refused, since dropping it could hide a
// check that failed.
func parseChecks(out []byte) ([]check, error) {
	var checks []check
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			return nil, fmt.Errorf("gh pr checks printed %q, which is not a check's name and bucket separated by a tab", line)
		}
		c := check{name: fields[0], bucket: fields[1]}
		if len(fields) > 2 {
			c.elapsed = fields[2]
		}
		if len(fields) > 3 {
			c.link = fields[3]
		}
		checks = append(checks, c)
	}
	return checks, nil
}

// passed reports whether a finished check's bucket counts as passing: a
// pass, or a check skipped because it never applied to a pull request.
func (c check) passed() bool {
	return c.bucket == bucketPass || c.bucket == bucketSkipping
}

// runWatchPR polls a pull request's checks every -every until none is
// pending, then records the run and prints the report's Actions-only and
// recurring sections. It returns an error naming the checks that failed,
// which exits 1.
func runWatchPR(args []string, stdout, stderr io.Writer, gh github) error {
	fs := flag.NewFlagSet("watch-pr", flag.ContinueOnError)
	dir := fs.String("dir", defaultDir, "the ledgers' directory")
	pr := fs.Int("pr", 0, "the pull request to watch")
	every := fs.Duration("every", 2*time.Minute, "how long to wait between asking GitHub")
	if err := flags(fs, args, stderr); err != nil {
		return err
	}
	if *pr <= 0 {
		return usageError{"-pr names the pull request to watch"}
	}
	if *every <= 0 {
		return usageError{"-every is how long to wait between polls, and must be more than zero"}
	}
	checks, err := waitForChecks(gh, *pr, *every, stdout, stderr)
	if err != nil {
		return err
	}
	var failed []string
	for _, c := range checks {
		if !c.passed() {
			failed = append(failed, c.name+" ("+c.bucket+")")
		}
	}
	sort.Strings(failed)
	fmt.Fprintf(stdout, "failures: every check of pull request %d has finished: %d passed or skipped, %d did not\n", *pr, len(checks)-len(failed), len(failed))

	// The recording and the report say what the ledgers know; neither
	// changes what the checks said, so a failure here is told and the
	// checks' answer still decides the exit status.
	if err := runRecordActions([]string{"-dir", *dir, "-pr", strconv.Itoa(*pr)}, stdout, stderr, gh); err != nil {
		fmt.Fprintf(stderr, "failures watch-pr: recording the run: %v\n", err)
	} else if err := printWatchReport(stdout, *dir); err != nil {
		fmt.Fprintf(stderr, "failures watch-pr: reading the ledgers: %v\n", err)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d check(s) of pull request %d did not pass: %s", len(failed), *pr, strings.Join(failed, ", "))
	}
	return nil
}

// waitForChecks asks gh for pr's checks until none is pending, printing
// each check once it finishes (again if a re-run changes its bucket), and
// returns the last answer. gh failing, or answering with no check, maxMisses
// times in a row ends the wait with that error.
func waitForChecks(gh github, pr int, every time.Duration, stdout, stderr io.Writer) ([]check, error) {
	printed := map[string]string{}
	misses := 0
	for {
		checks, err := askChecks(gh, pr)
		if err != nil {
			misses++
			if misses >= maxMisses {
				return nil, fmt.Errorf("gave up after %d answers in a row without a check: %w", misses, err)
			}
			fmt.Fprintf(stderr, "failures watch-pr: %v; asking again in %s\n", err, every)
			sleep(every)
			continue
		}
		misses = 0
		pending := 0
		for _, c := range checks {
			if c.bucket == bucketPending {
				pending++
				continue
			}
			if printed[c.name] != c.bucket {
				printed[c.name] = c.bucket
				fmt.Fprintf(stdout, "  %-9s %-28s %s\n", c.bucket, c.name, c.elapsed)
			}
		}
		if pending == 0 {
			return checks, nil
		}
		sleep(every)
	}
}

// errNoChecks is gh answering with no check at all, as it does before a
// pull request's workflow has started.
var errNoChecks = errors.New("no check is reported yet")

// askChecks is one poll: gh's answer, parsed, or why there is none.
func askChecks(gh github, pr int) ([]check, error) {
	out, err := gh.PRChecks(pr)
	if err != nil {
		return nil, err
	}
	checks, err := parseChecks(out)
	if err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("pull request %d: %w", pr, errNoChecks)
	}
	return checks, nil
}

// printWatchReport prints the crossover report's Actions-only and recurring
// sections from the ledgers under dir.
func printWatchReport(w io.Writer, dir string) error {
	entries, err := readAll(dir)
	if err != nil {
		return err
	}
	scopes, err := readScopes(scopeFile(dir))
	if err != nil {
		return err
	}
	r, err := buildReport(entries, scopes, Filters{}, now())
	if err != nil {
		return err
	}
	byName := summariesByName(r)
	printActionsOnly(w, r, byName)
	printRecurring(w, r, byName)
	return nil
}

// PRChecks runs gh pr checks for pr against this repository. gh 2.4.0 has
// no --watch and no JSON for it, prints one tab-separated line per check
// when its output is not a terminal, and exits 1 whenever a check is
// failing or pending while still printing every one, so what it printed is
// the answer and only an exit with nothing printed is an error.
func (ghCLI) PRChecks(pr int) ([]byte, error) {
	cmd := exec.Command("gh", "pr", "checks", strconv.Itoa(pr), "-R", repo) // #nosec G204 -- gh with a number and this repository's constant name
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("gh pr checks %d: %w: %s", pr, err, strings.TrimSpace(errOut.String()))
	}
	return out, nil
}

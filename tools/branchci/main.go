// Command branchci runs a branch's changed container packages on GitHub
// Actions' runners before the branch has a pull request: it dispatches the
// CI workflow's branch-containers job for the packages it is given, at the
// commit the branch's remote tip holds, follows the run to its end, and
// writes a receipt naming the commit and how the run concluded.
//
// It exists because this machine's Docker is Docker Desktop, a VM, while the
// runners run Docker Engine on their own kernel, and container tests that
// lean on the host passed here and failed first in a pull request
// (FAILURE_PATTERNS 426, 755). make branch-containers chooses the packages
// (the branch's scope, as pr-check reads it, narrowed to the container ones).
//
// Usage:
//
//	branchci -packages "pkg ..." [-receipt file] [-timeout 3h]
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// repo is the repository whose workflow is dispatched.
const repo = "Subject-Void-LLC/the-pleiades"

// workflow is the workflow file that holds the branch-containers job.
const workflow = "ci.yml"

// modulePath is the module every package must belong to.
const modulePath = "github.com/Subject-Void-LLC/the-pleiades"

// importPath is what a package argument may look like: this module's path
// and its directories, nothing a shell or the workflow could read as more.
var importPath = regexp.MustCompile(`^github\.com/Subject-Void-LLC/the-pleiades(/[A-Za-z0-9._-]+)*$`)

// options are the command line's.
type options struct {
	// packages are the import paths to test.
	packages []string
	// receipt is where the outcome is written.
	receipt string
	// timeout bounds the wait for the run.
	timeout time.Duration
	// poll is how often the run is read.
	poll time.Duration
}

func main() {
	if err := run(os.Args[1:], os.Stdout, ghCLI{}, gitCLI{}, time.Now, time.Sleep); err != nil {
		fmt.Fprintln(os.Stderr, "branchci:", err)
		os.Exit(1)
	}
}

// run parses args and does the work, reading GitHub through gh, the
// repository through git, the clock through now and waiting through sleep,
// so a test drives every path.
func run(args []string, out io.Writer, gh github, git repository, now func() time.Time, sleep func(time.Duration)) error {
	o, err := parse(args)
	if err != nil {
		return err
	}
	head, err := git.Head()
	if err != nil {
		return err
	}
	branch, err := git.Branch()
	if err != nil {
		return err
	}
	if branch == "" || branch == "main" {
		return fmt.Errorf("on %q: run it on a feature branch, which a pull request would come from", branch)
	}
	if len(o.packages) == 0 {
		fmt.Fprintln(out, "branchci: the branch changes no container package, so there is nothing to run")
		return writeReceipt(o.receipt, Receipt{Commit: head, Branch: branch, Conclusion: "nothing", WrittenAt: now().UTC()})
	}
	remote, err := git.RemoteTip(branch)
	if err != nil {
		return err
	}
	if remote != head {
		return fmt.Errorf("origin's %s is at %q and HEAD is %s: push the branch first (make pr-check, then git push), since the runners test what origin holds", branch, short(remote), short(head))
	}
	before, err := dispatchRuns(gh, branch)
	if err != nil {
		return err
	}
	if err := dispatch(gh, branch, o.packages); err != nil {
		return err
	}
	fmt.Fprintf(out, "branchci: dispatched %s's branch-containers job for %d package(s) at %s\n", workflow, len(o.packages), short(head))
	deadline := now().Add(o.timeout)
	var r Run
	for {
		r, err = newRun(gh, branch, head, before)
		if err != nil {
			return err
		}
		if r.ID != 0 {
			break
		}
		if now().After(deadline) {
			return fmt.Errorf("no run of the dispatch appeared within %s", o.timeout)
		}
		sleep(o.poll)
	}
	fmt.Fprintf(out, "branchci: following %s\n", r.URL)
	for r.Status != "completed" {
		if now().After(deadline) {
			return fmt.Errorf("run %d did not end within %s; it is still at %s", r.ID, o.timeout, r.URL)
		}
		sleep(o.poll)
		if r, err = readRun(gh, r.ID); err != nil {
			return err
		}
	}
	jobs, err := readJobs(gh, r.ID)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		fmt.Fprintf(out, "  %-28s %s  %s\n", j.Name, j.Conclusion, j.URL)
	}
	if err := writeReceipt(o.receipt, Receipt{Commit: head, Branch: branch, RunID: r.ID, URL: r.URL, Conclusion: r.Conclusion, Packages: o.packages, WrittenAt: now().UTC()}); err != nil {
		return err
	}
	if r.Conclusion != "success" {
		return fmt.Errorf("the run concluded %s: fix what failed before the pull request (%s)", r.Conclusion, r.URL)
	}
	fmt.Fprintf(out, "branchci: every package passed on the runners at %s\n", short(head))
	return nil
}

// parse reads the command line.
func parse(args []string) (options, error) {
	o := options{poll: 30 * time.Second}
	fs := flag.NewFlagSet("branchci", flag.ContinueOnError)
	var packages string
	fs.StringVar(&packages, "packages", "", "the container packages to test, import paths separated by spaces")
	fs.StringVar(&o.receipt, "receipt", ".git/pleiades-branch-ci.json", "where the outcome is written")
	fs.DurationVar(&o.timeout, "timeout", 3*time.Hour, "how long to wait for the run")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments %q: name the packages with -packages", fs.Args())
	}
	seen := map[string]bool{}
	for _, p := range strings.Fields(packages) {
		if !importPath.MatchString(p) {
			return o, fmt.Errorf("%q is not one of %s's packages", p, modulePath)
		}
		if !seen[p] {
			seen[p] = true
			o.packages = append(o.packages, p)
		}
	}
	if o.timeout <= 0 {
		return o, errors.New("-timeout must be above zero")
	}
	return o, nil
}

// short is a commit's first twelve characters.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// Receipt is what a finished run leaves for a reader: which commit was
// tested, by which run, and how it concluded.
type Receipt struct {
	// Commit is the commit the runners tested.
	Commit string `json:"commit"`
	// Branch is the branch it was on.
	Branch string `json:"branch"`
	// RunID and URL name the workflow run; zero and empty when nothing ran.
	RunID int64  `json:"run_id,omitempty"`
	URL   string `json:"url,omitempty"`
	// Conclusion is GitHub's (success, failure, cancelled...), or nothing
	// when the branch changes no container package.
	Conclusion string `json:"conclusion"`
	// Packages are the packages the run tested.
	Packages []string `json:"packages,omitempty"`
	// WrittenAt is when the receipt was written.
	WrittenAt time.Time `json:"written_at"`
}

// writeReceipt writes r to path as indented JSON.
func writeReceipt(path string, r Receipt) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// repository is the git questions this command asks.
type repository interface {
	// Head is HEAD's commit.
	Head() (string, error)
	// Branch is the checked-out branch, or "" when HEAD is detached.
	Branch() (string, error)
	// RemoteTip is the commit origin's branch holds, or "" when origin has
	// no such branch.
	RemoteTip(branch string) (string, error)
}

// gitCLI is repository through the git command.
type gitCLI struct{}

// Head runs git rev-parse HEAD.
func (gitCLI) Head() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

// Branch runs git symbolic-ref --short -q HEAD.
func (gitCLI) Branch() (string, error) {
	out, err := exec.Command("git", "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteTip asks origin for the branch with git ls-remote.
func (gitCLI) RemoteTip(branch string) (string, error) {
	out, err := exec.Command("git", "ls-remote", "origin", "refs/heads/"+branch).Output() // #nosec G204 -- git with a branch name, never a shell
	if err != nil {
		return "", fmt.Errorf("git ls-remote origin %s: %w", branch, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

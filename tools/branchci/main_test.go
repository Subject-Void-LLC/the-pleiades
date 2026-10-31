// branchci against a fake GitHub and a fake repository: the dispatch it
// sends, the run it finds and follows, the receipt it writes, and each
// refusal.
package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGit answers repository's questions from fields.
type fakeGit struct {
	head, branch, remote, dir string
}

// Head returns head.
func (g fakeGit) Head() (string, error) { return g.head, nil }

// Branch returns branch.
func (g fakeGit) Branch() (string, error) { return g.branch, nil }

// RemoteTip returns remote.
func (g fakeGit) RemoteTip(string) (string, error) { return g.remote, nil }

// GitDir returns dir.
func (g fakeGit) GitDir() (string, error) { return g.dir, nil }

// fakeGitHub is GitHub with one earlier dispatched run, which makes a new
// run when dispatched to and moves it to completed after polls reads. The
// new run is at head and named by the dispatch's nonce unless runHead or
// runTitle say otherwise, and the first fail GETs after the dispatch fail.
type fakeGitHub struct {
	t          *testing.T
	head       string
	conclusion string
	polls      int
	runHead    string
	runTitle   string
	fail       int
	dispatched []byte
	reads      int
}

// sentNonce is the nonce the dispatch carried.
func (f *fakeGitHub) sentNonce() string {
	var sent struct {
		Inputs map[string]string `json:"inputs"`
	}
	if err := json.Unmarshal(f.dispatched, &sent); err != nil {
		f.t.Fatal(err)
	}
	return sent.Inputs["nonce"]
}

// Get answers the runs list, a run and its jobs.
func (f *fakeGitHub) Get(path string) ([]byte, error) {
	if f.dispatched != nil && f.fail > 0 {
		f.fail--
		return nil, errors.New("HTTP 502")
	}
	switch {
	case strings.Contains(path, "/workflows/ci.yml/runs?"):
		runs := []Run{{ID: 1, HeadSHA: f.head, Title: "branch-containers old", Status: "completed", Conclusion: "failure"}}
		if f.dispatched != nil {
			r := Run{ID: 2, HeadSHA: f.head, Title: runName(f.sentNonce()), Status: "queued", URL: "https://example/run/2"}
			if f.runHead != "" {
				r.HeadSHA = f.runHead
			}
			if f.runTitle != "" {
				r.Title = f.runTitle
			}
			runs = append([]Run{r}, runs...)
		}
		return json.Marshal(map[string]any{"workflow_runs": runs})
	case strings.HasSuffix(path, "/actions/runs/2"):
		f.reads++
		r := Run{ID: 2, HeadSHA: f.head, Status: "in_progress", URL: "https://example/run/2"}
		if f.reads >= f.polls {
			r.Status, r.Conclusion = "completed", f.conclusion
		}
		return json.Marshal(r)
	case strings.Contains(path, "/actions/runs/2/jobs"):
		return json.Marshal(map[string]any{"jobs": []Job{{Name: "branch-containers (1)", Conclusion: f.conclusion, URL: "https://example/job"}}})
	}
	f.t.Fatalf("unexpected GET %s", path)
	return nil, nil
}

// Post records the dispatch.
func (f *fakeGitHub) Post(path string, body []byte) ([]byte, error) {
	if !strings.HasSuffix(path, "/actions/workflows/ci.yml/dispatches") {
		f.t.Fatalf("unexpected POST %s", path)
	}
	f.dispatched = body
	return nil, nil
}

// clock is a clock that moves only when slept.
type clock struct{ now time.Time }

// Now returns the time.
func (c *clock) Now() time.Time { return c.now }

// Sleep moves the time on.
func (c *clock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

// nonce is the fixed nonce the tests dispatch with.
func nonce() (string, error) { return "00112233aabbccdd", nil }

// pushed is a feature branch whose origin tip is HEAD.
var pushed = fakeGit{head: head, branch: "feat/x", remote: head}

const (
	head = "0123456789abcdef0123456789abcdef01234567"
	pkgA = "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/clab"
	pkgB = "github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// readReceipt reads the receipt at path.
func readReceipt(t *testing.T, path string) Receipt {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Receipt
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRun_DispatchesFollowsAndRecords: the dispatch names the branch and the
// packages once each, the run followed is the new one, and the receipt names
// the commit and the conclusion; a failed run is an error with its receipt.
func TestRun_DispatchesFollowsAndRecords(t *testing.T) {
	for _, conclusion := range []string{"success", "failure"} {
		t.Run(conclusion, func(t *testing.T) {
			gh := &fakeGitHub{t: t, head: head, conclusion: conclusion, polls: 3}
			c := &clock{now: time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)}
			receipt := filepath.Join(t.TempDir(), "r.json")
			var out strings.Builder
			err := run([]string{"-packages", pkgA + " " + pkgB + " " + pkgA, "-receipt", receipt}, &out, gh, pushed, nonce, c.Now, c.Sleep)
			if (err == nil) != (conclusion == "success") {
				t.Fatalf("err = %v for a run that concluded %s\n%s", err, conclusion, out.String())
			}
			var sent struct {
				Ref    string            `json:"ref"`
				Inputs map[string]string `json:"inputs"`
			}
			if jerr := json.Unmarshal(gh.dispatched, &sent); jerr != nil {
				t.Fatal(jerr)
			}
			if sent.Ref != "feat/x" || sent.Inputs["packages"] != pkgA+" "+pkgB || sent.Inputs["nonce"] != "00112233aabbccdd" {
				t.Errorf("dispatched %+v", sent)
			}
			r := readReceipt(t, receipt)
			if r.Commit != head || r.RunID != 2 || r.Conclusion != conclusion || len(r.Packages) != 2 {
				t.Errorf("receipt %+v", r)
			}
			if !strings.Contains(out.String(), "branch-containers (1)") {
				t.Errorf("the jobs were not listed:\n%s", out.String())
			}
		})
	}
}

// TestRun_Refusals: an unpushed branch, main and a detached HEAD are refused
// before any dispatch and before any receipt, with packages or without, and
// a branch with no container package writes a receipt saying so in the git
// directory and dispatches nothing, but only once it is pushed.
func TestRun_Refusals(t *testing.T) {
	c := &clock{now: time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)}
	for name, git := range map[string]fakeGit{
		"not pushed":  {head: head, branch: "feat/x", remote: "ffff"},
		"main":        {head: head, branch: "main", remote: head},
		"detached":    {head: head, remote: head},
		"never there": {head: head, branch: "feat/x"},
	} {
		for _, args := range [][]string{{"-packages", pkgA}, nil} {
			receipt := filepath.Join(t.TempDir(), "r.json")
			gh := &fakeGitHub{t: t, head: head}
			if err := run(append(args, "-receipt", receipt), io.Discard, gh, git, nonce, c.Now, c.Sleep); err == nil {
				t.Errorf("%s %v: accepted", name, args)
			}
			if gh.dispatched != nil {
				t.Errorf("%s %v: dispatched", name, args)
			}
			if _, err := os.Stat(receipt); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s %v: wrote a receipt (%v)", name, args, err)
			}
		}
	}

	git := pushed
	git.dir = t.TempDir()
	gh := &fakeGitHub{t: t, head: head}
	if err := run(nil, io.Discard, gh, git, nonce, c.Now, c.Sleep); err != nil {
		t.Fatal(err)
	}
	r := readReceipt(t, filepath.Join(git.dir, "pleiades-branch-ci.json"))
	if gh.dispatched != nil || r.Conclusion != "nothing" || r.Commit != head {
		t.Errorf("no packages: dispatched %s, receipt %+v", gh.dispatched, r)
	}
}

// TestRun_FollowsOnlyItsOwnRun: a new run at another commit (the branch
// moved before the dispatch) or named by another dispatch's nonce is never
// followed, so the command times out and leaves no receipt, the earlier one
// removed, rather than recording a run that is not its own; a run that never
// ends times out the same way.
func TestRun_FollowsOnlyItsOwnRun(t *testing.T) {
	for name, gh := range map[string]*fakeGitHub{
		"another commit": {runHead: "fedcba9876543210fedcba9876543210fedcba98"},
		"another nonce":  {runTitle: "branch-containers ffffffffffffffff"},
		"a nightly":      {runTitle: "CI"},
		"never ends":     {polls: 1 << 30},
	} {
		gh.t, gh.head, gh.conclusion = t, head, "success"
		c := &clock{now: time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)}
		receipt := filepath.Join(t.TempDir(), "r.json")
		if err := os.WriteFile(receipt, []byte(`{"commit":"earlier","conclusion":"success"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		err := run([]string{"-packages", pkgA, "-receipt", receipt, "-timeout", "2m"}, io.Discard, gh, pushed, nonce, c.Now, c.Sleep)
		want := "no run of the dispatch appeared"
		if name == "never ends" {
			want = "did not end"
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
		if _, err := os.Stat(receipt); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: a receipt is left (%v)", name, err)
		}
	}
}

// TestRun_ReadsOfGitHubMayFailBriefly: fewer than tries failed reads in a
// row are waited out, both finding the run and following it; tries of them
// end the command with the error.
func TestRun_ReadsOfGitHubMayFailBriefly(t *testing.T) {
	for _, fail := range []int{tries - 1, tries} {
		gh := &fakeGitHub{t: t, head: head, conclusion: "success", polls: 2, fail: fail}
		c := &clock{now: time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)}
		err := run([]string{"-packages", pkgA, "-receipt", filepath.Join(t.TempDir(), "r.json")}, io.Discard, gh, pushed, nonce, c.Now, c.Sleep)
		if (err == nil) != (fail < tries) {
			t.Errorf("%d failed reads: %v", fail, err)
		}
	}
	// The same, once the run is found: the reads of the run fail.
	gh := &fakeGitHub{t: t, head: head, conclusion: "success", polls: 3}
	flaky := &failAfter{github: gh, after: 2, fail: tries - 1}
	c := &clock{now: time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)}
	if err := run([]string{"-packages", pkgA, "-receipt", filepath.Join(t.TempDir(), "r.json")}, io.Discard, flaky, pushed, nonce, c.Now, c.Sleep); err != nil {
		t.Errorf("following through %d failed reads: %v", tries-1, err)
	}
}

// failAfter is a github whose GETs after the first after succeed fail fail
// times in a row, then answer again.
type failAfter struct {
	github
	after, fail, gets int
}

// Get fails as failAfter says, else asks github.
func (f *failAfter) Get(path string) ([]byte, error) {
	f.gets++
	if f.gets > f.after && f.fail > 0 {
		f.fail--
		return nil, errors.New("HTTP 502")
	}
	return f.github.Get(path)
}

// TestNewNonce: a nonce is sixteen hex characters and two differ.
func TestNewNonce(t *testing.T) {
	a, err := newNonce()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newNonce()
	if len(a) != 16 || a == b || strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("nonces %q and %q", a, b)
	}
}

// TestParse refuses what is not one of the module's import paths, stray
// arguments and a timeout of zero.
func TestParse(t *testing.T) {
	for name, args := range map[string][]string{
		"another module":  {"-packages", "github.com/other/repo"},
		"a shell word":    {"-packages", pkgA + ";rm"},
		"a stray arg":     {"-packages", pkgA, "extra"},
		"a zero timeout":  {"-packages", pkgA, "-timeout", "0s"},
		"an unknown flag": {"-nope"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	o, err := parse([]string{"-packages", " " + pkgA + "  " + pkgB + " "})
	if err != nil || len(o.packages) != 2 {
		t.Errorf("parse = %+v, %v", o, err)
	}
}

// TestGitHubErrors: an error from GitHub at each step is returned, and an
// answer that is not JSON is named.
func TestGitHubErrors(t *testing.T) {
	boom := errors.New("boom")
	if _, err := listRuns(errGitHub{err: boom}, "b"); !errors.Is(err, boom) {
		t.Errorf("listRuns: %v", err)
	}
	if _, err := listRuns(errGitHub{body: "nope"}, "b"); err == nil {
		t.Error("listRuns read text that is not JSON")
	}
	if _, err := readRun(errGitHub{body: "nope"}, 1); err == nil {
		t.Error("readRun read text that is not JSON")
	}
	if _, err := readJobs(errGitHub{body: "nope"}, 1); err == nil {
		t.Error("readJobs read text that is not JSON")
	}
	if err := dispatch(errGitHub{err: boom}, "b", []string{pkgA}, "n"); !errors.Is(err, boom) {
		t.Errorf("dispatch: %v", err)
	}
	if !strings.Contains(runsPath("feat/a b"), "branch=feat%2Fa+b") {
		t.Errorf("the branch is not escaped: %s", runsPath("feat/a b"))
	}
}

// errGitHub answers every call with body or err.
type errGitHub struct {
	body string
	err  error
}

// Get answers with body or err.
func (e errGitHub) Get(string) ([]byte, error) { return []byte(e.body), e.err }

// Post answers with body or err.
func (e errGitHub) Post(string, []byte) ([]byte, error) { return []byte(e.body), e.err }

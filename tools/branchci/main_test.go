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
	head, branch, remote string
}

// Head returns head.
func (g fakeGit) Head() (string, error) { return g.head, nil }

// Branch returns branch.
func (g fakeGit) Branch() (string, error) { return g.branch, nil }

// RemoteTip returns remote.
func (g fakeGit) RemoteTip(string) (string, error) { return g.remote, nil }

// fakeGitHub is GitHub with one earlier dispatched run, which makes a new
// run when dispatched to and moves it to completed after polls reads.
type fakeGitHub struct {
	t          *testing.T
	head       string
	conclusion string
	polls      int
	dispatched []byte
	reads      int
}

// Get answers the runs list, a run and its jobs.
func (f *fakeGitHub) Get(path string) ([]byte, error) {
	switch {
	case strings.Contains(path, "/workflows/ci.yml/runs?"):
		runs := []Run{{ID: 1, HeadSHA: f.head, Status: "completed", Conclusion: "failure"}}
		if f.dispatched != nil {
			runs = append([]Run{{ID: 2, HeadSHA: f.head, Status: "queued", URL: "https://example/run/2"}}, runs...)
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
			err := run([]string{"-packages", pkgA + " " + pkgB + " " + pkgA, "-receipt", receipt}, &out, gh, fakeGit{head: head, branch: "feat/x", remote: head}, c.Now, c.Sleep)
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
			if sent.Ref != "feat/x" || sent.Inputs["packages"] != pkgA+" "+pkgB {
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
// before any dispatch, a branch with no container package writes a receipt
// saying so and dispatches nothing, and a run that never ends times out.
func TestRun_Refusals(t *testing.T) {
	c := &clock{now: time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)}
	pkgs := []string{"-packages", pkgA, "-receipt", filepath.Join(t.TempDir(), "r.json")}
	for name, git := range map[string]fakeGit{
		"not pushed":  {head: head, branch: "feat/x", remote: "ffff"},
		"main":        {head: head, branch: "main", remote: head},
		"detached":    {head: head, remote: head},
		"never there": {head: head, branch: "feat/x"},
	} {
		gh := &fakeGitHub{t: t, head: head}
		if err := run(pkgs, io.Discard, gh, git, c.Now, c.Sleep); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if gh.dispatched != nil {
			t.Errorf("%s: dispatched", name)
		}
	}

	receipt := filepath.Join(t.TempDir(), "none.json")
	gh := &fakeGitHub{t: t, head: head}
	if err := run([]string{"-receipt", receipt}, io.Discard, gh, fakeGit{head: head, branch: "feat/x", remote: head}, c.Now, c.Sleep); err != nil {
		t.Fatal(err)
	}
	if gh.dispatched != nil || readReceipt(t, receipt).Conclusion != "nothing" {
		t.Errorf("no packages: dispatched %s, receipt %+v", gh.dispatched, readReceipt(t, receipt))
	}

	slow := &fakeGitHub{t: t, head: head, conclusion: "success", polls: 1 << 30}
	err := run([]string{"-packages", pkgA, "-receipt", filepath.Join(t.TempDir(), "r.json"), "-timeout", "2m"}, io.Discard, slow, fakeGit{head: head, branch: "feat/x", remote: head}, c.Now, c.Sleep)
	if err == nil || !strings.Contains(err.Error(), "did not end") {
		t.Errorf("a run that never ends: %v", err)
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
	if err := dispatch(errGitHub{err: boom}, "b", []string{pkgA}); !errors.Is(err, boom) {
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

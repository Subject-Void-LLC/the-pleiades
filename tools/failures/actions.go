// Entries from a GitHub Actions workflow run, through gh api: each job that
// ran, its log read for testgate's failures and contention, and a failed
// job with no test to blame recorded as a leg.
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// repo is the repository whose runs are read.
const repo = "Subject-Void-LLC/the-pleiades"

// github answers a GitHub REST path, as gh api does. It is an interface so a
// test answers from fixtures and needs no network.
type github interface {
	// API returns the body GitHub answers path with.
	API(path string) ([]byte, error)
}

// ghCLI is github through the gh command, logged in as its user.
type ghCLI struct{}

// API runs gh api path.
func (ghCLI) API(path string) ([]byte, error) {
	out, err := exec.Command("gh", "api", path).Output() // #nosec G204 -- gh with a path this tool built from numbers
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api %s: %w: %s", path, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh api %s: %w", path, err)
	}
	return out, nil
}

// workflowRun is the part of a run this tool reads.
type workflowRun struct {
	ID         int64     `json:"id"`
	HeadSHA    string    `json:"head_sha"`
	HeadBranch string    `json:"head_branch"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// job is the part of a job this tool reads.
type job struct {
	ID          int64     `json:"id"`
	RunID       int64     `json:"run_id"`
	Name        string    `json:"name"`
	Conclusion  string    `json:"conclusion"`
	CompletedAt time.Time `json:"completed_at"`
	Labels      []string  `json:"labels"`
	HeadSHA     string    `json:"head_sha"`
	HeadBranch  string    `json:"head_branch"`
}

// latestRunForPR is the id of the newest completed workflow run on pull
// request pr's head commit.
func latestRunForPR(gh github, pr int) (int64, error) {
	body, err := gh.API(fmt.Sprintf("repos/%s/pulls/%d", repo, pr))
	if err != nil {
		return 0, err
	}
	var p struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Head.SHA == "" {
		return 0, fmt.Errorf("reading pull request %d: no head commit in its answer", pr)
	}
	body, err = gh.API(fmt.Sprintf("repos/%s/actions/runs?head_sha=%s&per_page=50", repo, p.Head.SHA))
	if err != nil {
		return 0, err
	}
	var runs struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &runs); err != nil {
		return 0, fmt.Errorf("reading the runs of %s: %w", p.Head.SHA, err)
	}
	sort.Slice(runs.Runs, func(i, j int) bool { return runs.Runs[i].CreatedAt.After(runs.Runs[j].CreatedAt) })
	for _, r := range runs.Runs {
		if r.Status == "completed" {
			return r.ID, nil
		}
	}
	return 0, fmt.Errorf("pull request %d's head %s has no completed run", pr, p.Head.SHA)
}

// recordedConclusions are the job conclusions whose logs are read: a job
// that passed can still hold contention, and a skipped one ran nothing.
var recordedConclusions = map[string]bool{"success": true, "failure": true, "timed_out": true, "cancelled": true}

// fromRun reads run's jobs and each one's log into entries. failedOnly reads
// only the jobs that did not pass, which misses their contention.
func fromRun(gh github, run int64, failedOnly bool, now time.Time) ([]Entry, error) {
	body, err := gh.API(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, run))
	if err != nil {
		return nil, err
	}
	var jobs struct {
		Jobs []job `json:"jobs"`
	}
	if err := json.Unmarshal(body, &jobs); err != nil {
		return nil, fmt.Errorf("reading run %d's jobs: %w", run, err)
	}
	var out []Entry
	for _, j := range jobs.Jobs {
		if !recordedConclusions[j.Conclusion] || (failedOnly && j.Conclusion == "success") {
			continue
		}
		log, err := gh.API(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, j.ID))
		if err != nil {
			return nil, err
		}
		out = append(out, fromJob(j, parseJobLog(string(log)), now)...)
	}
	return out, nil
}

// fromJob turns one job's parsed log into entries: each failure testgate
// listed, and for a job that did not pass with none counted against it, one
// leg entry saying why.
func fromJob(j job, l jobLog, now time.Time) []Entry {
	base := Entry{Schema: EntrySchema, Source: SourceActions, RecordedAt: now.UTC(), SeenAt: j.CompletedAt.UTC(),
		Commit: j.HeadSHA, Branch: j.HeadBranch, RunID: j.RunID, JobID: j.ID, Leg: j.Name, OS: runnerOS(j.Labels)}
	var out []Entry
	counted := false
	for _, f := range l.failures {
		e := base
		e.Kind, e.Package, e.Test, e.Verdict, e.Reason, e.Error = KindTest, scrub(f.pkg), scrub(f.test), f.verdict, f.reason, f.err
		if f.verdict != VerdictContention {
			counted = true
		}
		out = append(out, e)
	}
	if j.Conclusion != "success" && !counted {
		e := base
		e.Kind, e.Verdict = KindLeg, VerdictFail
		if j.Conclusion == "cancelled" {
			e.Verdict = VerdictNotRun
		}
		e.Reason = l.reason
		if e.Reason == "" {
			e.Reason = "the job's conclusion is " + j.Conclusion + ", and its log says no more"
		}
		out = append(out, e)
	}
	return out
}

// runnerOS is the OS a job's runner label names, in the words pr-check uses.
func runnerOS(labels []string) string {
	for _, l := range labels {
		switch {
		case strings.HasPrefix(l, "ubuntu"):
			return "linux"
		case strings.HasPrefix(l, "macos"):
			return "macOS"
		case strings.HasPrefix(l, "windows"):
			return "Windows"
		}
	}
	if len(labels) > 0 {
		return scrub(labels[0])
	}
	return ""
}

// GitHub through gh api: the dispatch, the runs a dispatch makes, and a
// run's jobs.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// github answers GitHub REST calls, as gh api does. It is an interface so a
// test answers from fixtures and needs no network.
type github interface {
	// Get returns the body GitHub answers a GET of path with.
	Get(path string) ([]byte, error)
	// Post sends body to path and returns GitHub's answer.
	Post(path string, body []byte) ([]byte, error)
}

// ghCLI is github through the gh command, logged in as its user.
type ghCLI struct{}

// Get runs gh api path.
func (ghCLI) Get(path string) ([]byte, error) {
	return ghAPI(exec.Command("gh", "api", path)) // #nosec G204 -- gh with a path this tool built
}

// Post runs gh api -X POST path with body on its standard input.
func (ghCLI) Post(path string, body []byte) ([]byte, error) {
	cmd := exec.Command("gh", "api", "-X", "POST", path, "--input", "-") // #nosec G204 -- gh with a path this tool built
	cmd.Stdin = bytes.NewReader(body)
	return ghAPI(cmd)
}

// ghAPI runs a gh api command and names gh's own words when it fails.
func ghAPI(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return out, nil
}

// Run is the part of a workflow run this command reads.
type Run struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
}

// Job is the part of a run's job this command reads.
type Job struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
}

// dispatch asks GitHub to run the workflow on branch with packages as its
// packages input.
func dispatch(gh github, branch string, packages []string) error {
	body, err := json.Marshal(map[string]any{"ref": branch, "inputs": map[string]string{"packages": strings.Join(packages, " ")}})
	if err != nil {
		return err
	}
	_, err = gh.Post(fmt.Sprintf("repos/%s/actions/workflows/%s/dispatches", repo, workflow), body)
	return err
}

// runsPath lists the workflow's dispatched runs on branch, newest first.
func runsPath(branch string) string {
	return fmt.Sprintf("repos/%s/actions/workflows/%s/runs?event=workflow_dispatch&branch=%s&per_page=30", repo, workflow, url.QueryEscape(branch))
}

// listRuns reads the workflow's dispatched runs on branch.
func listRuns(gh github, branch string) ([]Run, error) {
	b, err := gh.Get(runsPath(branch))
	if err != nil {
		return nil, err
	}
	var page struct {
		Runs []Run `json:"workflow_runs"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		return nil, fmt.Errorf("reading the runs: %w", err)
	}
	return page.Runs, nil
}

// dispatchRuns is the set of dispatched runs on branch before this
// command's own dispatch, so the run it makes is the one not in it.
func dispatchRuns(gh github, branch string) (map[int64]bool, error) {
	runs, err := listRuns(gh, branch)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(runs))
	for _, r := range runs {
		seen[r.ID] = true
	}
	return seen, nil
}

// newRun is the dispatched run on branch at head that was not there before,
// or a zero Run while GitHub has not made it yet.
func newRun(gh github, branch, head string, before map[int64]bool) (Run, error) {
	runs, err := listRuns(gh, branch)
	if err != nil {
		return Run{}, err
	}
	for _, r := range runs {
		if !before[r.ID] && r.HeadSHA == head {
			return r, nil
		}
	}
	return Run{}, nil
}

// readRun reads one run.
func readRun(gh github, id int64) (Run, error) {
	b, err := gh.Get(fmt.Sprintf("repos/%s/actions/runs/%d", repo, id))
	if err != nil {
		return Run{}, err
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return Run{}, fmt.Errorf("reading run %d: %w", id, err)
	}
	return r, nil
}

// readJobs reads a run's jobs.
func readJobs(gh github, id int64) ([]Job, error) {
	b, err := gh.Get(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, id))
	if err != nil {
		return nil, err
	}
	var page struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		return nil, fmt.Errorf("reading run %d's jobs: %w", id, err)
	}
	return page.Jobs, nil
}

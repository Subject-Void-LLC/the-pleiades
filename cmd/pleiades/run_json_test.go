// End-to-end tests for adhoc and run --json, through the built binary: the
// document on standard output, what it says, and the exit status beside it.
package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// jsonReport is the documented shape of a --json document, decoded
// strictly: a field the command stops writing, or renames, fails here.
type jsonReport struct {
	Runbook          string `json:"runbook"`
	Mode             string `json:"mode"`
	Nodes            int    `json:"nodes"`
	InventoryHosts   int    `json:"inventory_hosts"`
	ServiceEffecting bool   `json:"service_effecting"`
	BlastRadius      struct {
		Devices int      `json:"devices"`
		Tiers   []string `json:"tiers"`
	} `json:"blast_radius"`
	Selection  string     `json:"selection"`
	Plan       []jsonPlan `json:"plan"`
	Validation []struct {
		Rule    string `json:"rule"`
		Node    string `json:"node"`
		Device  string `json:"device"`
		Message string `json:"message"`
	} `json:"validation"`
	Tasks []struct {
		ID               string         `json:"id"`
		Name             string         `json:"name"`
		Method           string         `json:"method"`
		Device           string         `json:"device"`
		Host             string         `json:"host"`
		Status           string         `json:"status"`
		Error            string         `json:"error"`
		Reason           string         `json:"reason"`
		AllowedUnchecked bool           `json:"allowed_unchecked"`
		Warnings         []string       `json:"warnings"`
		Provider         map[string]any `json:"provider"`
		Stats            map[string]any `json:"stats"`
		StartedAt        *time.Time     `json:"started_at"`
		FinishedAt       *time.Time     `json:"finished_at"`
	} `json:"tasks"`
	Metadata map[string]any `json:"metadata"`
	RunID    string         `json:"run_id"`
	Journal  string         `json:"journal"`
	// Rollback is a rollback's own part of the report, kept raw: the
	// rollback gates decode what they read from it themselves.
	Rollback json.RawMessage `json:"rollback"`
	Outcome  struct {
		Status           string   `json:"status"`
		Message          string   `json:"message"`
		ExitCode         int      `json:"exit_code"`
		Unchecked        int      `json:"unchecked"`
		AllowedUnchecked []string `json:"allowed_unchecked"`
	} `json:"outcome"`
}

// jsonPlan is one section of a report's plan.
type jsonPlan struct {
	Section string         `json:"section"`
	Tasks   []jsonPlanTask `json:"tasks"`
}

// jsonPlanTask is one planned task, with a group's own tasks under it.
type jsonPlanTask struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Method   string         `json:"method"`
	Kind     string         `json:"kind"`
	Selected bool           `json:"selected"`
	Block    []jsonPlanTask `json:"block"`
	Rescue   []jsonPlanTask `json:"rescue"`
	Always   []jsonPlanTask `json:"always"`
	Parallel []jsonPlanTask `json:"parallel"`
}

// runPleiadesJSON runs the binary with --json and decodes its standard
// output, which must hold exactly one document whatever the exit status.
// It returns the raw output too, for a test that searches it.
func runPleiadesJSON(t *testing.T, dir string, args ...string) (jsonReport, string, int) {
	t.Helper()
	return runPleiadesJSONWithHome(t, dir, "", args...)
}

// runPleiadesJSONWithHome is runPleiadesJSON with HOME set to home, so the
// binary reads that home's known_hosts; "" keeps this process's HOME.
func runPleiadesJSONWithHome(t *testing.T, dir, home string, args ...string) (jsonReport, string, int) {
	t.Helper()
	cmd := exec.Command(binPath, append(args, "--json")...)
	cmd.Dir = dir
	if home != "" {
		cmd.Env = append(os.Environ(), "HOME="+home)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("pleiades did not run: %v", err)
		}
		code = exitErr.ExitCode()
	}
	var rep jsonReport
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("%v: standard output is not a report: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	if dec.More() {
		t.Fatalf("%v: standard output holds more than one document:\n%s", args, stdout.String())
	}
	if rep.Outcome.ExitCode != code {
		t.Errorf("%v: the report says exit %d and the command exited %d", args, rep.Outcome.ExitCode, code)
	}
	return rep, stdout.String(), code
}

// adhocProject is a project with two devices tagged web.
func adhocProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "web01", "--type", "linux_server", "--set", "host=10.0.0.5", "--tags", "web"},
		{"add-host", "web02", "--type", "linux_server", "--set", "host=10.0.0.6", "--tags", "web"},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestCLI_AdhocRunsOneMethodOnATag(t *testing.T) {
	dir := adhocProject(t)

	out, err := runPleiades(t, dir, "adhoc", "web", "noop", "msg=hello", "--verbose")
	if err != nil || !strings.Contains(out, "plan for adhoc noop on web") || strings.Count(out, "msg: hello") != 2 || !strings.Contains(out, "run complete") {
		t.Fatalf("text view: %v\n%s", err, out)
	}

	rep, _, code := runPleiadesJSON(t, dir, "adhoc", "web", "noop", "msg=hello")
	if code != 0 || rep.Runbook != "adhoc noop on web" || rep.Mode != "execute" || rep.Outcome.Status != "complete" {
		t.Fatalf("exit %d, report %+v", code, rep)
	}
	hosts := map[string]bool{}
	for _, task := range rep.Tasks {
		if task.Status != "ok" || task.Method != "noop" || task.Stats["msg"] != "hello" || task.StartedAt == nil {
			t.Errorf("task %+v", task)
		}
		hosts[task.Host] = true
	}
	if len(rep.Tasks) != 2 || !hosts["web01"] || !hosts["web02"] {
		t.Errorf("ran on %v, want web01 and web02", hosts)
	}
	if len(rep.Plan) != 1 || rep.Plan[0].Tasks[0].Method != "noop" || !rep.Plan[0].Tasks[0].Selected {
		t.Errorf("plan %+v", rep.Plan)
	}

	// Both real runs are journaled, under the ad-hoc runbook id.
	journals, _ := filepath.Glob(filepath.Join(dir, ".pleiades", "journal", "*.jsonl"))
	if len(journals) != 2 {
		t.Fatalf("%d journals after two runs", len(journals))
	}
	if data, err := os.ReadFile(journals[0]); err != nil || !strings.Contains(string(data), `"dag_id":"adhoc"`) {
		t.Errorf("the journal does not name the ad-hoc run: %v\n%s", err, data)
	}

	// The report names the run and its journal, and the journal it names
	// is the one on disk, sealed: the run returned, so every level it ran
	// was recorded.
	if rep.RunID == "" || rep.Journal != filepath.Join(dir, ".pleiades", "journal", rep.RunID+".jsonl") {
		t.Errorf("report names run %q journal %q, want the run's own file under %s", rep.RunID, rep.Journal, dir)
	}
	if _, err := os.Stat(rep.Journal); err != nil {
		t.Errorf("the journal the report names: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pleiades", "journal", rep.RunID+".end")); err != nil {
		t.Errorf("the finished run left no seal: %v", err)
	}
	// The text view prints the same, for the run it made.
	textRun := regexp.MustCompile(`run ([0-9a-f-]{36}), journal (\S+)`).FindStringSubmatch(out)
	if textRun == nil || textRun[2] != filepath.Join(dir, ".pleiades", "journal", textRun[1]+".jsonl") {
		t.Errorf("text view does not name its run and journal:\n%s", out)
	}

	// A check changes nothing and journals nothing.
	rep, _, code = runPleiadesJSON(t, dir, "adhoc", "web", "noop", "--mode", "check")
	if code != 0 || rep.Mode != "check" || rep.Outcome.Message != "check complete: nothing was changed" {
		t.Errorf("check: exit %d, %+v", code, rep.Outcome)
	}
	if rep.RunID != "" || rep.Journal != "" {
		t.Errorf("a check named run %q journal %q, but it writes no journal", rep.RunID, rep.Journal)
	}
	if again, _ := filepath.Glob(filepath.Join(dir, ".pleiades", "journal", "*.jsonl")); len(again) != 2 {
		t.Errorf("a check wrote a journal")
	}
}

func TestCLI_AdhocRefusalsAreReports(t *testing.T) {
	dir := adhocProject(t)
	for name, c := range map[string]struct {
		args   []string
		status string
		want   string
	}{
		"a task keyword":    {[]string{"adhoc", "web", "when"}, "invalid", "task keyword"},
		"a malformed param": {[]string{"adhoc", "web", "noop", "justtext"}, "invalid", "key=value"},
		"an unknown param":  {[]string{"adhoc", "web", "exec.command", "cmd=uptime", "bogus=1"}, "invalid", "validation failed"},
		"an unknown method": {[]string{"adhoc", "web", "no.such.method"}, "invalid", "validation failed"},
		"no such device":    {[]string{"adhoc", "nosuch", "noop"}, "failed", "execution failed"},
		"a missing runbook": {[]string{"run", "runbooks/missing.yaml"}, "error", "missing.yaml"},
	} {
		rep, _, code := runPleiadesJSON(t, dir, c.args...)
		if code != 1 || rep.Outcome.Status != c.status || !strings.Contains(rep.Outcome.Message, c.want) {
			t.Errorf("%s: exit %d, outcome %+v, want %s naming %q", name, code, rep.Outcome, c.status, c.want)
		}
		if c.want == "validation failed" && len(rep.Validation) == 0 {
			t.Errorf("%s: no validation findings in the report", name)
		}
	}
}

// TestCLI_RunJSONMasksSecrets is TestCLI_RunMasksRegisterMask for the
// JSON view: a value marked secret appears nowhere in the document, not
// even in a later task's error that echoes it.
func TestCLI_RunJSONMasksSecrets(t *testing.T) {
	dir := adhocProject(t)
	const secret = "sup3r-secret-password"
	runbook := "id: secret-demo\n" +
		"tasks:\n" +
		"  - name: mark-secret\n" +
		"    noop:\n" +
		"      password: \"" + secret + "\"\n" +
		"    register: creds\n" +
		"    register_mask: [password]\n" +
		"  - name: leak-secret\n" +
		"    noop:\n" +
		"      target: \"" + secret + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "secret.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, raw, code := runPleiadesJSON(t, dir, "run", "runbooks/secret.yaml")
	if strings.Contains(raw, secret) {
		t.Fatalf("the secret is in the document:\n%s", raw)
	}
	if code != 1 || rep.Outcome.Status != "failed" || len(rep.Tasks) != 2 {
		t.Fatalf("exit %d, report %+v", code, rep)
	}
	if failed := rep.Tasks[1]; failed.Status != "failed" || !strings.Contains(failed.Error, "********") {
		t.Errorf("the failed task reads %+v, want its error with the secret masked", failed)
	}
	// A stat named password is a secret by its name, whatever it holds.
	if got := rep.Tasks[0].Stats["password"]; got != "$encrypted$" {
		t.Errorf("the password stat reads %v", got)
	}
}

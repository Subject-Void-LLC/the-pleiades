// Driving the machine a check runs on through this branch's own pleiades
// binary: building it, and calling one method at a time with adhoc --json.
package oscheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Pleiades is this branch's pleiades binary and the device it drives, in
// the project that holds that device.
type Pleiades struct {
	// Binary is where Build writes the binary.
	Binary string
	// Project is the project directory every call names with --dir.
	Project string
	// Device is the machine's name in Project's inventory.
	Device string
}

// Build builds pleiades from this tree into p.Binary, so a check drives
// the branch's own binary.
func (p Pleiades) Build() error {
	out, err := exec.Command("go", "build", "-o", p.Binary, "./cmd/pleiades").CombinedOutput() // #nosec G204 -- this repository's own command
	if err != nil {
		return fmt.Errorf("building pleiades: %v\n%s", err, out)
	}
	return nil
}

// Run runs the binary with args, returning its combined output.
func (p Pleiades) Run(args ...string) (string, error) {
	out, err := exec.Command(p.Binary, args...).CombinedOutput() // #nosec G204 -- the binary this check built, with arguments it wrote
	return string(out), err
}

// adhocReport is the part of `pleiades adhoc --json` read here.
type adhocReport struct {
	Tasks      []reportTask `json:"tasks"`
	Validation []struct {
		Message string `json:"message"`
	} `json:"validation"`
	Outcome struct {
		ExitCode int    `json:"exit_code"`
		Message  string `json:"message"`
	} `json:"outcome"`
}

// Adhoc runs one method against the device, its pleiades ended when ctx
// ends, and returns the task's stdout return, or its error.
func (p Pleiades) Adhoc(ctx context.Context, method string, params ...string) (string, error) {
	out := p.adhocJSON(ctx, method, params...)
	return adhocStdout(method, out)
}

// AdhocStats runs one method against the device, its pleiades ended when
// ctx ends, and returns the task's stats, or its error.
func (p Pleiades) AdhocStats(ctx context.Context, method string, params ...string) (map[string]any, error) {
	out := p.adhocJSON(ctx, method, params...)
	return adhocTaskStats(method, out)
}

// adhocJSON runs one method against the device with adhoc --json, its
// pleiades ended when ctx ends, and returns the report it printed: again,
// while the report says the device never answered (retryUnreached).
func (p Pleiades) adhocJSON(ctx context.Context, method string, params ...string) []byte {
	args := append([]string{"adhoc", p.Device, method}, params...)
	args = append(args, "--json", "--dir", p.Project)
	return retryUnreached(ctx, UnreachedRetryFor, waitFor, func() []byte {
		out, _ := exec.CommandContext(ctx, p.Binary, args...).Output() // #nosec G204 -- the binary this check built, with arguments it wrote
		return out
	})
}

// UnreachedRetryFor bounds how long a call whose device never answered is
// tried again, counted in the waits between tries. A lab box resumed from
// its saved state can miss connections for one to two minutes after it
// starts running (win-check, measured 2026-10-19: from about 30 s to
// 90-130 s after virt.vbox.vm.start began, with answers in between, so a
// short streak of logins can pass inside that window).
const UnreachedRetryFor = 3 * time.Minute

// The waits between tries of an unreached call: the first, doubling to the
// longest.
const (
	unreachedFirstWait = 2 * time.Second
	unreachedLongWait  = 30 * time.Second
)

// waitFor waits d, or until ctx ends, and reports whether d passed.
func waitFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// retryUnreached calls call, and calls it again while the report it returns
// says its one task's device never answered (the report's unreached mark),
// waiting between tries with wait, until the waits would pass bound or ctx
// ends; it returns the last report. Only an unreached task is tried again:
// nothing was sent, so nothing runs twice. A refused login, a command that
// failed and a task that started are answers, and are returned as they are.
func retryUnreached(ctx context.Context, bound time.Duration, wait func(context.Context, time.Duration) bool, call func() []byte) []byte {
	delay, waited := unreachedFirstWait, time.Duration(0)
	for {
		out := call()
		if !reportUnreached(out) || ctx.Err() != nil || waited+delay > bound {
			return out
		}
		if !wait(ctx, delay) {
			return out
		}
		waited += delay
		delay = min(2*delay, unreachedLongWait)
	}
}

// reportUnreached reports whether out is an adhoc --json report whose one
// task failed with its device never answering.
func reportUnreached(out []byte) bool {
	var report adhocReport
	return json.Unmarshal(out, &report) == nil && len(report.Tasks) == 1 && report.Tasks[0].Unreached
}

// adhocTaskStats reads one adhoc --json report: the one task's stats, or
// why there are none, a failed task's own error included.
func adhocTaskStats(method string, out []byte) (map[string]any, error) {
	task, failed, err := oneTask(method, out)
	if err != nil {
		return nil, err
	}
	if failed != "" {
		return nil, fmt.Errorf("%s: %s", method, failed)
	}
	return task.Stats, nil
}

// adhocStdout reads one adhoc --json report: the one task's stdout return,
// or why there is none, a failed task's own error included.
func adhocStdout(method string, out []byte) (string, error) {
	task, failed, err := oneTask(method, out)
	if err != nil {
		return "", err
	}
	if failed != "" {
		return "", fmt.Errorf("%s: %s", method, failed)
	}
	stdout, _ := task.Stats["stdout"].(string)
	return stdout, nil
}

// reportTask is one task of an adhoc --json report.
type reportTask struct {
	Status string         `json:"status"`
	Error  string         `json:"error"`
	Stats  map[string]any `json:"stats"`
	// Unreached marks a failed task whose device never answered.
	Unreached bool `json:"unreached"`
}

// oneTask reads one adhoc --json report's one task, and why it failed (""
// when it did not), or why the report has no one task.
func oneTask(method string, out []byte) (reportTask, string, error) {
	var report adhocReport
	if err := json.Unmarshal(out, &report); err != nil {
		return reportTask{}, "", fmt.Errorf("pleiades adhoc %s printed no report: %v", method, err)
	}
	if len(report.Tasks) != 1 {
		why := report.Outcome.Message
		for _, v := range report.Validation {
			why += "; " + v.Message
		}
		return reportTask{}, "", fmt.Errorf("pleiades adhoc %s ran %d tasks: %s", method, len(report.Tasks), why)
	}
	task := report.Tasks[0]
	if task.Error != "" || report.Outcome.ExitCode != 0 {
		return task, FirstOf(task.Error, report.Outcome.Message), nil
	}
	return task, "", nil
}

// Outcome is a command's answer: what it wrote to standard output, and the
// exit status it ended with.
type Outcome struct {
	Output   string
	ExitCode int
}

// AdhocOutcome runs a command method (exec.command, exec.winrm.shell)
// against the device, its pleiades ended when ctx ends, and returns its
// output and exit status, a non-zero one included: a failed task keeps
// the exit status and output its method recorded (rc, or exit_code), so a
// command that ran and failed is an answer, and only one that did not run
// is an error.
func (p Pleiades) AdhocOutcome(ctx context.Context, method string, params ...string) (Outcome, error) {
	out := p.adhocJSON(ctx, method, params...)
	return adhocOutcome(method, out)
}

// exitStats are the stats a command method records its exit status
// under: exec.command's rc, and exec.winrm.shell's exit_code.
var exitStats = []string{"rc", "exit_code"}

// adhocOutcome reads one adhoc --json report: the one task's output and
// exit status, or why it has none.
func adhocOutcome(method string, out []byte) (Outcome, error) {
	task, failed, err := oneTask(method, out)
	if err != nil {
		return Outcome{}, err
	}
	stdout, _ := task.Stats["stdout"].(string)
	if failed == "" {
		return Outcome{Output: stdout}, nil
	}
	for _, key := range exitStats {
		// JSON gives every number as a float64.
		if code, ok := task.Stats[key].(float64); ok && code != 0 {
			return Outcome{Output: stdout, ExitCode: int(code)}, nil
		}
	}
	return Outcome{}, fmt.Errorf("%s: %s", method, failed)
}

// ArgvYAML is argv as a YAML flow list, for an adhoc key:=yaml parameter
// (exec.command's argv).
func ArgvYAML(argv ...string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = quote(a)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// MapYAML is m as a YAML flow mapping, its keys in sorted order, for an
// adhoc key:=yaml parameter (exec.winrm.shell's env).
func MapYAML(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = quote(k) + ": " + quote(m[k])
	}
	return "{" + strings.Join(pairs, ", ") + "}"
}

// quote is s as a YAML double-quoted scalar, which a JSON string is, so
// every character reaches the method as it was.
func quote(s string) string {
	b, _ := json.Marshal(s) // a string always marshals
	return string(b)
}

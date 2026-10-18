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
	args := append([]string{"adhoc", p.Device, method}, params...)
	args = append(args, "--json", "--dir", p.Project)
	out, _ := exec.CommandContext(ctx, p.Binary, args...).Output() // #nosec G204 -- the binary this check built, with arguments it wrote
	return adhocStdout(method, out)
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
	args := append([]string{"adhoc", p.Device, method}, params...)
	args = append(args, "--json", "--dir", p.Project)
	out, _ := exec.CommandContext(ctx, p.Binary, args...).Output() // #nosec G204 -- the binary this check built, with arguments it wrote
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

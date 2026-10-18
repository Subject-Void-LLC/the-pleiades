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
	Tasks []struct {
		Status string         `json:"status"`
		Error  string         `json:"error"`
		Stats  map[string]any `json:"stats"`
	} `json:"tasks"`
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
// or why there is none.
func adhocStdout(method string, out []byte) (string, error) {
	var report adhocReport
	if err := json.Unmarshal(out, &report); err != nil {
		return "", fmt.Errorf("pleiades adhoc %s printed no report: %v", method, err)
	}
	if len(report.Tasks) != 1 {
		why := report.Outcome.Message
		for _, v := range report.Validation {
			why += "; " + v.Message
		}
		return "", fmt.Errorf("pleiades adhoc %s ran %d tasks: %s", method, len(report.Tasks), why)
	}
	task := report.Tasks[0]
	if task.Error != "" || report.Outcome.ExitCode != 0 {
		return "", fmt.Errorf("%s: %s", method, FirstOf(task.Error, report.Outcome.Message))
	}
	stdout, _ := task.Stats["stdout"].(string)
	return stdout, nil
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

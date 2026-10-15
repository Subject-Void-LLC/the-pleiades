// Package win implements win.schtask: a Windows scheduled task
// registered, updated, removed or inspected over WinRM
// (docs/design/small-methods.md). The script it runs is a constant
// (schtaskScript) and every value reaches it as JSON on standard input,
// so nothing a task names can become PowerShell syntax.
package win

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

const fqcnSchtask = "win.schtask"

// Parameters.
const (
	paramName        = "name"
	paramState       = "state"
	paramExecute     = "execute"
	paramArguments   = "arguments"
	paramWorkingDir  = "working_directory"
	paramTrigger     = "trigger"
	paramAt          = "at"
	paramRunAs       = "run_as"
	paramRunLevel    = "run_level"
	paramEnabled     = "enabled"
	paramDescription = "description"
	paramXML         = "xml"
)

// maxAnswer bounds the script's answer (a task's XML is a few KiB).
const maxAnswer = 1 << 20

var (
	// segment is one folder or the name of a task path.
	segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,199}$`)
	// account is a user a task may run as.
	account   = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,64}(\\[A-Za-z0-9 ._$-]{1,104})?$`)
	clockTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	triggers  = []string{"at_startup", "at_logon", "daily", "once"}
)

// serviceAccounts run as a service account, with no password and no
// interactive logon.
var serviceAccounts = map[string]bool{"SYSTEM": true, "LOCAL SERVICE": true, "NETWORK SERVICE": true}

// run is how the script runs; a test replaces it.
var run = func(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, stdin string, opts winrmexec.Options) (winrmexec.Result, error) {
	return winrmexec.RunWithStdin(ctx, target, auth, winrmexec.ShellPowerShell, schtaskScript, stdin, opts)
}

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnSchtask,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportWinRM},
			RequiredCapabilities: []capability.Name{capability.NameWinRM},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A task this run registered is removed by its undo. A task it changed or removed is registered again from the XML " +
					"definition it had, read before the change; that XML is withheld from the journal, so a rollback replays it only from " +
					"the task's own rollback: list. The task's run history is not restored.",
				Inverses: []sdk.InverseSpec{{FQCN: fqcnSchtask, Record: []string{paramName, paramState}, Withhold: []string{paramXML}}},
			},
			Doc: schtaskDoc(),
		},
		Invoke: Schtask,
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return schtask(ctx, rc, d, p, true)
		},
	})
}

// spec is a call: what to do with which task, and for state present, the
// task as it should be.
type spec struct {
	state, path, name                  string
	execute, arguments, workingDir     string
	trigger, at, user, logon, runLevel string
	description, xml                   string
	enabled                            bool
}

// parse reads a call, holding every value to its form.
func parse(params map[string]any) (spec, error) {
	var s spec
	full, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return s, err
	}
	parts := strings.Split(strings.TrimPrefix(full, `\`), `\`)
	for _, p := range parts {
		if !segment.MatchString(p) {
			return s, fmt.Errorf("%s %q is not a task path: folders and a name of letters, digits, space, '.', '_' and '-', separated by \\", paramName, full)
		}
	}
	s.name, s.path = parts[len(parts)-1], `\`+strings.Join(parts[:len(parts)-1], `\`)
	if !strings.HasSuffix(s.path, `\`) {
		s.path += `\`
	}
	s.state = sdk.StringParam(params, paramState)
	if s.state == "" {
		s.state = "present"
	}
	switch s.state {
	case "query", "absent":
		return s, nil
	case "present":
	default:
		return s, fmt.Errorf("%s %q is not present, absent or query", paramState, s.state)
	}
	if s.xml = sdk.StringParam(params, paramXML); s.xml != "" {
		if len(s.xml) > maxAnswer || !strings.Contains(s.xml, "<Task") {
			return s, fmt.Errorf("%s is not a scheduled task's XML definition", paramXML)
		}
		return s, nil
	}
	if s.execute, err = sdk.RequiredStringParam(params, paramExecute); err != nil {
		return s, fmt.Errorf("%w (or give %s, a task's XML definition)", err, paramXML)
	}
	s.arguments, s.workingDir, s.description = sdk.StringParam(params, paramArguments), sdk.StringParam(params, paramWorkingDir), sdk.StringParam(params, paramDescription)
	for _, f := range []struct {
		name, value string
		max         int
	}{{paramExecute, s.execute, 260}, {paramArguments, s.arguments, 8000}, {paramWorkingDir, s.workingDir, 260}, {paramDescription, s.description, 1000}} {
		if len(f.value) > f.max || strings.ContainsAny(f.value, "\x00\r\n") {
			return s, fmt.Errorf("%s is over %d characters or holds a line break", f.name, f.max)
		}
	}
	s.trigger, s.at = sdk.StringParam(params, paramTrigger), sdk.StringParam(params, paramAt)
	switch s.trigger {
	case "at_startup", "at_logon":
		if s.at != "" {
			return s, fmt.Errorf("%s goes with daily or once, not %s", paramAt, s.trigger)
		}
	case "daily":
		if !clockTime.MatchString(s.at) {
			return s, fmt.Errorf("a daily trigger's %s is a time, HH:MM", paramAt)
		}
	case "once":
		if _, err := time.Parse(time.RFC3339, s.at); err != nil {
			return s, fmt.Errorf("a once trigger's %s is an RFC 3339 time", paramAt)
		}
	default:
		return s, fmt.Errorf("%s is one of %v", paramTrigger, triggers)
	}
	s.user = sdk.StringParam(params, paramRunAs)
	if s.user == "" {
		s.user = "SYSTEM"
	}
	if !account.MatchString(s.user) {
		return s, fmt.Errorf("%s %q is not an account name", paramRunAs, s.user)
	}
	s.logon = "s4u"
	if serviceAccounts[strings.ToUpper(s.user)] {
		s.logon, s.user = "service", strings.ToUpper(s.user)
	}
	switch s.runLevel = sdk.StringParam(params, paramRunLevel); s.runLevel {
	case "", "limited":
		s.runLevel = "Limited"
	case "highest":
		s.runLevel = "Highest"
	default:
		return s, fmt.Errorf("%s is limited or highest", paramRunLevel)
	}
	if s.enabled, err = sdk.BoolParamOr(params, paramEnabled, true); err != nil {
		return s, err
	}
	return s, nil
}

// described is the script's answer: a task as it is.
type described struct {
	Exists      bool   `json:"exists"`
	XML         string `json:"xml"`
	Enabled     bool   `json:"enabled"`
	State       string `json:"state"`
	Description string `json:"description"`
	Execute     string `json:"execute"`
	Arguments   string `json:"arguments"`
	WorkingDir  string `json:"working_directory"`
	Actions     int    `json:"actions"`
	Triggers    int    `json:"triggers"`
	Trigger     struct {
		Kind  string `json:"kind"`
		Start string `json:"start"`
	} `json:"trigger"`
	User           string `json:"user"`
	RunLevel       string `json:"run_level"`
	LastRunTime    string `json:"last_run_time"`
	LastTaskResult string `json:"last_task_result"`
	NextRunTime    string `json:"next_run_time"`
}

// differences lists how the task as it is differs from s; none means it
// is as asked.
func (d described) differences(s spec) []string {
	if !d.Exists {
		return []string{"the task does not exist"}
	}
	var out []string
	add := func(field, got, want string) {
		if got != want {
			out = append(out, fmt.Sprintf("%s is %q, not %q", field, got, want))
		}
	}
	if d.Actions != 1 || d.Triggers != 1 {
		out = append(out, fmt.Sprintf("it has %d actions and %d triggers, not one of each", d.Actions, d.Triggers))
	}
	add(paramExecute, d.Execute, s.execute)
	add(paramArguments, d.Arguments, s.arguments)
	add(paramWorkingDir, d.WorkingDir, s.workingDir)
	add(paramDescription, d.Description, s.description)
	add(paramTrigger, triggerOf(d.Trigger.Kind), s.trigger)
	switch s.trigger {
	case "daily":
		if !strings.Contains(d.Trigger.Start, "T"+s.at+":") {
			out = append(out, fmt.Sprintf("the trigger starts at %q, not at %s", d.Trigger.Start, s.at))
		}
	case "once":
		want, _ := time.Parse(time.RFC3339, s.at)
		if got, err := time.Parse(time.RFC3339, d.Trigger.Start); err != nil || !got.Equal(want) {
			if got, err := time.ParseInLocation("2006-01-02T15:04:05", d.Trigger.Start, time.Local); err != nil || !got.Equal(want) {
				out = append(out, fmt.Sprintf("the trigger starts at %q, not at %s", d.Trigger.Start, s.at))
			}
		}
	}
	if !strings.EqualFold(lastPart(d.User), lastPart(s.user)) {
		out = append(out, fmt.Sprintf("it runs as %q, not %q", d.User, s.user))
	}
	add(paramRunLevel, d.RunLevel, s.runLevel)
	if d.Enabled != s.enabled {
		out = append(out, fmt.Sprintf("enabled is %v, not %v", d.Enabled, s.enabled))
	}
	return out
}

// triggerOf names a CIM trigger class as the trigger parameter does.
func triggerOf(class string) string {
	switch class {
	case "MSFT_TaskBootTrigger":
		return "at_startup"
	case "MSFT_TaskLogonTrigger":
		return "at_logon"
	case "MSFT_TaskDailyTrigger":
		return "daily"
	case "MSFT_TaskTimeTrigger":
		return "once"
	}
	return class
}

// lastPart is an account without its domain.
func lastPart(user string) string {
	if i := strings.LastIndex(user, `\`); i >= 0 {
		return user[i+1:]
	}
	return user
}

// session is the device's WinRM target and this run's credential.
func session(rc sdk.RunbookContext, device inventory.InventoryItem) (winrmexec.Target, winrmexec.Auth, winrmexec.Options, error) {
	dev, ok := device.(capability.WinRMCapable)
	if !ok {
		return winrmexec.Target{}, winrmexec.Auth{}, winrmexec.Options{}, errors.New("the device is not reached over WinRM")
	}
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return winrmexec.Target{}, winrmexec.Auth{}, winrmexec.Options{}, err
	}
	return winrmexec.Target{Host: dev.WinRMHost(), Port: dev.WinRMPort()}, auth, winrmexec.WithDeviceTLS(winrmexec.Options{}, devicetls.For(device)), nil
}

// call runs the script for op with s, and reads its answer.
func call(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, opts winrmexec.Options, op string, s spec) (described, error) {
	in, err := json.Marshal(map[string]any{
		"op": op, "path": s.path, "name": s.name, "execute": s.execute, "arguments": s.arguments, "working_directory": s.workingDir,
		"trigger": s.trigger, "at": s.at, "user": s.user, "logon": s.logon, "run_level": s.runLevel, "enabled": s.enabled,
		"description": s.description, "xml": s.xml,
	})
	if err != nil {
		return described{}, err
	}
	res, err := run(ctx, target, auth, string(in), opts)
	if err != nil {
		return described{}, err
	}
	if res.ExitCode != 0 {
		return described{}, fmt.Errorf("%s exited %d: %s", op, res.ExitCode, bounded(res.Stderr))
	}
	if len(res.Stdout) > maxAnswer {
		return described{}, fmt.Errorf("the answer to %s is over %d bytes", op, maxAnswer)
	}
	var d described
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &d); err != nil {
		return described{}, fmt.Errorf("the answer to %s is not the task's description: %w", op, err)
	}
	return d, nil
}

// bounded cuts PowerShell's error text to 1 KiB.
func bounded(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1024 {
		return s[:1024] + "..."
	}
	return s
}

// Schtask implements win.schtask.
func Schtask(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return schtask(ctx, rc, device, params, false)
}

func schtask(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnSchtask, err)
	}
	s, err := parse(params)
	if err != nil {
		return fail(err)
	}
	target, auth, opts, err := session(rc, device)
	if err != nil {
		return fail(err)
	}
	before, err := call(ctx, target, auth, opts, "query", s)
	if err != nil {
		return fail(err)
	}
	full := s.path + s.name
	stats := map[string]any{"name": full, "existed": before.Exists, "state": before.State, "last_run_time": before.LastRunTime,
		"last_task_result": before.LastTaskResult, "next_run_time": before.NextRunTime}
	var changed bool
	var diffs []string
	switch {
	case s.state == "query":
	case s.state == "absent":
		changed = before.Exists
	case s.xml != "":
		changed = !before.Exists || before.XML != s.xml
	default:
		diffs = before.differences(s)
		changed = len(diffs) > 0
	}
	stats["differences"] = toAny(diffs)
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return fail(err)
		}
	}
	if check || !changed {
		return collection.Result{Changed: changed}, nil
	}
	op := "register"
	switch {
	case s.state == "absent":
		op = "remove"
	case s.xml != "":
		op = "restore"
	}
	after, err := call(ctx, target, auth, opts, op, s)
	if err != nil {
		return fail(err)
	}
	if s.state == "present" && !after.Exists {
		return fail(errors.New("the task was registered and is not there"))
	}
	undo := sdk.Inverse{FQCN: fqcnSchtask, Params: map[string]any{paramName: full, paramState: "absent"}, Description: "Remove " + full + ", which this run registered."}
	if before.Exists {
		undo = sdk.Inverse{FQCN: fqcnSchtask, Params: map[string]any{paramName: full, paramState: "present", paramXML: before.XML},
			Description: "Register " + full + " again from the definition it had before this run."}
	}
	return collection.Result{Changed: true}, sdk.RecordInverse(rc, undo)
}

// toAny is a string list as a return value.
func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func schtaskDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Registers, updates or removes a Windows scheduled task over WinRM, or reports one as it is.",
		Description: "Reads the task first (Get-ScheduledTask, its XML definition and its last and next run), then: with state present, registers it with one action, one trigger and its principal when it is missing or differs in any of them, its description or whether it is enabled, and is no change when it matches; with absent, removes it; with query, only reports it. The script is a constant and every value reaches it as JSON on standard input, so a value can never be PowerShell syntax; every name is held to a strict pattern first. run_as SYSTEM, LOCAL SERVICE or NETWORK SERVICE runs as that service account; another account runs with S4U logon, so no password is stored with the task, and the task cannot reach the network as that user. A task this run registered undoes to its removal; one it changed or removed undoes to registering its earlier XML definition, withheld from the journal (xml, which also registers a task from a definition you give). A check reads and reports what would change.",
		Params: []collection.Param{
			{Name: paramName, Type: "string", Required: true, Description: "The task's path: \\Folder\\Name, or Name in the root folder."},
			{Name: paramState, Type: "string", Default: "present", Choices: []string{"present", "absent", "query"}, Description: "present registers or updates it; absent removes it; query only reports it."},
			{Name: paramExecute, Type: "string", Description: "The program the task runs. Required with state present, unless xml is given."},
			{Name: paramArguments, Type: "string", Description: "Its arguments, as one string."},
			{Name: paramWorkingDir, Type: "string", Description: "The directory it starts in."},
			{Name: paramTrigger, Type: "string", Choices: triggers, Description: "When it runs: at_startup, at_logon, daily (at, HH:MM, the device's local time) or once (at, an RFC 3339 time). Required with state present."},
			{Name: paramAt, Type: "string", Description: "The time of a daily or once trigger."},
			{Name: paramRunAs, Type: "string", Default: "SYSTEM", Description: "The account it runs as: SYSTEM, LOCAL SERVICE, NETWORK SERVICE, or an account (DOMAIN\\name or name) run with S4U logon and no stored password."},
			{Name: paramRunLevel, Type: "string", Default: "limited", Choices: []string{"limited", "highest"}, Description: "Whether it runs with the account's highest privileges."},
			{Name: paramEnabled, Type: "bool", Default: "true", Description: "Whether the task is enabled."},
			{Name: paramDescription, Type: "string", Description: "The task's description."},
			{Name: paramXML, Type: "string", Description: "A whole task definition, as Export-ScheduledTask writes it, registered as given with state present; what this method's own undo uses."},
		},
		Returns: []collection.ReturnField{
			{Name: "name", Type: "string", Returned: "always", Description: "The task's full path."},
			{Name: "existed", Type: "bool", Returned: "always", Description: "Whether the task existed before."},
			{Name: "state", Type: "string", Returned: "always", Description: "Its state before (Ready, Running, Disabled), empty when it did not exist."},
			{Name: "last_run_time", Type: "string", Returned: "always", Description: "When it last ran."},
			{Name: "last_task_result", Type: "string", Returned: "always", Description: "Its last result code."},
			{Name: "next_run_time", Type: "string", Returned: "always", Description: "When it runs next."},
			{Name: "differences", Type: "list of string", Returned: "always", Description: "How it differed from what was asked, empty when it matched."},
		},
		Examples: []collection.Example{
			{Name: "A nightly cleanup", RunbookYAML: "- name: Cleanup\n  win.schtask:\n    name: \\Pleiades\\Cleanup\n    execute: C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe\n    arguments: -NoProfile -File C:\\ops\\cleanup.ps1\n    trigger: daily\n    at: \"02:30\"\n"},
			{Name: "Is the backup task healthy", RunbookYAML: "- name: Backup task\n  win.schtask:\n    name: \\Pleiades\\Backup\n    state: query\n  register: backup\n"},
		},
		SeeAlso: []string{"exec.winrm.shell", "svc.windows.start"},
	}
}

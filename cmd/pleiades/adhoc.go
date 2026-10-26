// The adhoc command: one method against a device or a tag, with no
// runbook written, Ansible's `ansible <hosts> -m <module> -a <args>`.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// adhocCommand is adhoc's positionals, the start of its usage line.
const adhocCommand = "adhoc <hosts | a,b,c | -> <method> [key=value | key:=yaml ...]"

// noHosts is the hosts adhoc takes for a method that needs no device.
const noHosts = "-"

// adhocRunbookID is the id an ad-hoc run is journaled under.
const adhocRunbookID = "adhoc"

// paramKey is what a parameter's name may be: the lowercase words with
// underscores every method's parameters use.
var paramKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// runAdhoc runs one method against hosts, a device name or an inventory
// tag exactly as a runbook's hosts: takes one, with the parameters given
// after it.
//
// It writes the one-task runbook a person would have written, in memory,
// and hands it to the pipeline run uses (run_pipeline.go), so it is parsed,
// validated, checked, journaled and reported exactly as that runbook
// would be. There is no second execution path to keep in step.
func runAdhoc(args []string) error {
	fs := flag.NewFlagSet("adhoc", flag.ContinueOnError)
	flags := addRunFlags(fs)
	var timeout secondsFlag
	fs.Var(&timeout, "timeout", "how long the call may run on each device before it is stopped and fails, as a task's timeout key: whole seconds (40) or a duration (40s, 5m), up to 86400 seconds (0: no bound)")
	positionals, rest := splitPositionals(args, runBoolFlags)
	// A method that needs no device may be named first, with no hosts.
	if len(positionals) >= 1 && needsNoDevice(positionals[0]) && (len(positionals) == 1 || strings.Contains(positionals[1], "=")) {
		positionals = append([]string{noHosts}, positionals...)
	}
	if len(positionals) < 2 {
		return fmt.Errorf("%s: %w", usageLine(adhocCommand, fs), errMissingPositional)
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	opts, err := flags.options()
	if err != nil {
		return err
	}
	hosts, method := positionals[0], positionals[1]
	label := adhocLabel(hosts, method)
	params, err := parseAdhocParams(positionals[2:], declaredTypes(method))
	if err != nil {
		return refuseAdhoc(opts, label, err, fmt.Errorf("%w\n%s", err, usageLine(adhocCommand, fs)))
	}
	payload, err := adhocRunbook(hosts, method, params, int(timeout))
	if err != nil {
		return refuseAdhoc(opts, label, err, err)
	}
	return runPipeline(opts, runSource{label: label, yaml: payload})
}

// secondsFlag is a number of whole seconds, written as a number (40) or as
// a Go duration (40s, 5m), since both are how people write a timeout.
type secondsFlag int

// String implements flag.Value.
func (s *secondsFlag) String() string { return strconv.Itoa(int(*s)) }

// Set implements flag.Value, refusing a fraction of a second, which a
// task's timeout key cannot hold.
func (s *secondsFlag) Set(text string) error {
	n, err := engine.ParseSeconds(text)
	if err != nil {
		return err
	}
	*s = secondsFlag(n)
	return nil
}

// refuseAdhoc ends an ad-hoc run refused before it started. With --json,
// the refusal is still a report on standard output, since a program
// composing the call is the one most likely to get a parameter wrong and
// should not have to read stderr to learn which; why is what that report
// says, and shown is what the command ends with.
func refuseAdhoc(opts runOptions, label string, why, shown error) error {
	if opts.asJSON {
		rep := &runReport{Runbook: label, Mode: opts.mode, Outcome: runOutcome{Status: "invalid", Message: why.Error(), ExitCode: 1}}
		if err := writeJSON(os.Stdout, rep); err != nil {
			return err
		}
	}
	return shown
}

// needsNoDevice reports whether name is a registered method that runs with
// no device (collection.DeviceNone), which adhoc then takes without hosts.
func needsNoDevice(name string) bool {
	d, ok := collection.Lookup(name)
	return ok && d.Manifest.ExecutionContext.Device == collection.DeviceNone
}

// adhocLabel is how the plan and the report name an ad-hoc run.
func adhocLabel(hosts, method string) string {
	if hosts == noHosts {
		return fmt.Sprintf("adhoc %s, on no device", method)
	}
	return fmt.Sprintf("adhoc %s on %s", method, hosts)
}

// parseAdhocParams reads the parameters given after the method.
//
// key=value keeps value as text when the method declares that parameter a
// string (declared, from its manifest), so mode=0644 is the text 0644 its
// author wrote rather than a number the method must then refuse. For any
// other parameter it types value as add-host --set does: true and false are
// booleans, a whole base-10 number is an integer, and anything else is a
// string, a dotted version included. key:=value reads value as YAML, for
// what key=value cannot say: a list ([a, b]) or a map ({X: 1}). A value
// keeps any = it holds, since only the first one ends the key.
func parseAdhocParams(tokens []string, declared map[string]string) (map[string]any, error) {
	params := map[string]any{}
	for _, token := range tokens {
		key, raw, ok := strings.Cut(token, "=")
		if !ok {
			return nil, fmt.Errorf("a parameter is key=value or key:=yaml, got %q", token)
		}
		structured := strings.HasSuffix(key, ":")
		key = strings.TrimSuffix(key, ":")
		if !paramKey.MatchString(key) {
			return nil, fmt.Errorf("%q is not a parameter name: a name is letters, digits and underscores", key)
		}
		if _, repeated := params[key]; repeated {
			return nil, fmt.Errorf("parameter %s is given twice", key)
		}
		if !structured {
			if declared[key] == "string" {
				params[key] = raw
			} else {
				params[key] = parsePropertyValue(raw)
			}
			continue
		}
		var value any
		if err := yaml.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("parameter %s:= is not YAML: %w", key, err)
		}
		params[key] = value
	}
	return params, nil
}

// declaredTypes returns the type each of method's parameters declares in
// its manifest, or nil for a method not registered yet (an external one
// loaded later), whose values are then typed as add-host --set types them.
func declaredTypes(method string) map[string]string {
	d, ok := collection.Lookup(method)
	if !ok {
		return nil
	}
	types := make(map[string]string, len(d.Manifest.Doc.Params))
	for _, p := range d.Manifest.Doc.Params {
		types[p.Name] = p.Type
	}
	return types
}

// adhocRunbook is the runbook an ad-hoc run carries out: one task calling
// method on hosts with params, and timeout as the task's own timeout key
// when it is not zero, written as YAML by an encoder, never by joining
// strings, so no value can change the runbook's shape. The builder holds
// the timeout to its range, as it would in a file.
//
// hosts "-" writes no hosts: at all, for a method that needs no device. A
// comma list of devices (r1,r2) writes one task per device, each naming its
// device as its target:, since a runbook's hosts: takes one name or tag.
//
// A method named as one of a task's own keys (name, when, block and the
// rest) would be read as that key rather than as a method, so it is
// refused here; every other name is left to the runbook parser and
// validation, which refuse it exactly as they would in a file.
func adhocRunbook(hosts, method string, params map[string]any, timeout int) ([]byte, error) {
	switch {
	case hosts == "":
		return nil, errors.New("adhoc needs a device or a tag to run on")
	case method == "":
		return nil, errors.New("adhoc needs a method to run")
	case engine.ReservedTaskKeys[method] || method == "import_tasks":
		return nil, fmt.Errorf("%q is a task keyword, not a method; name a method, like exec.command", method)
	}
	task := func(name, target string) (map[string]any, error) {
		t := map[string]any{"name": name, method: nil}
		p := make(map[string]any, len(params)+1)
		for k, v := range params {
			p[k] = v
		}
		if target != "" {
			if _, given := p[collection.TargetParam]; given {
				return nil, fmt.Errorf("a comma list of hosts names each task's %s itself; give the hosts or %s=, not both", collection.TargetParam, collection.TargetParam)
			}
			p[collection.TargetParam] = target
		}
		if len(p) > 0 {
			t[method] = p
		}
		if timeout != 0 {
			t["timeout"] = timeout
		}
		return t, nil
	}
	runbookHosts := hosts
	var tasks []map[string]any
	switch {
	case hosts == noHosts:
		runbookHosts = ""
		t, err := task(method, "")
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	case strings.Contains(hosts, ","):
		runbookHosts = ""
		seen := map[string]bool{}
		for _, h := range strings.Split(hosts, ",") {
			h = strings.TrimSpace(h)
			if h == "" || seen[h] {
				return nil, fmt.Errorf("hosts %q holds an empty or repeated name", hosts)
			}
			seen[h] = true
			t, err := task(method+" on "+h, h)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, t)
		}
	default:
		t, err := task(method, "")
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return yaml.Marshal(struct {
		ID    string           `yaml:"id"`
		Name  string           `yaml:"name"`
		Hosts string           `yaml:"hosts,omitempty"`
		Tasks []map[string]any `yaml:"tasks"`
	}{ID: adhocRunbookID, Name: adhocLabel(hosts, method), Hosts: runbookHosts, Tasks: tasks})
}

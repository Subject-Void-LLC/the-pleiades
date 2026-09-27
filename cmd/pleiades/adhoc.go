// The adhoc command: one method against a device or a tag, with no
// runbook written, Ansible's `ansible <hosts> -m <module> -a <args>`.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// adhocUsage is adhoc's usage line, for its errors.
const adhocUsage = "usage: pleiades adhoc <hosts> <method> [key=value | key:=yaml ...] [--mode execute|check] [--forks 5] [--persist-connections=false] [--verbose] [--json] [--dir .]"

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
	positionals, rest := splitPositionals(args, runBoolFlags)
	if len(positionals) < 2 {
		return fmt.Errorf("%s: %w", adhocUsage, errMissingPositional)
	}
	fs := flag.NewFlagSet("adhoc", flag.ContinueOnError)
	flags := addRunFlags(fs)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	opts, err := flags.options()
	if err != nil {
		return err
	}
	hosts, method := positionals[0], positionals[1]
	label := adhocLabel(hosts, method)
	params, err := parseAdhocParams(positionals[2:])
	if err != nil {
		return refuseAdhoc(opts, label, err, fmt.Errorf("%w\n%s", err, adhocUsage))
	}
	payload, err := adhocRunbook(hosts, method, params)
	if err != nil {
		return refuseAdhoc(opts, label, err, err)
	}
	return runPipeline(opts, runSource{label: label, yaml: payload})
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

// adhocLabel is how the plan and the report name an ad-hoc run.
func adhocLabel(hosts, method string) string {
	return fmt.Sprintf("adhoc %s on %s", method, hosts)
}

// parseAdhocParams reads the parameters given after the method.
//
// key=value types value as add-host --set does: true and false are
// booleans, a whole base-10 number is an integer, and anything else is a
// string, a dotted version included. key:=value reads value as YAML, for
// what key=value cannot say: a list ([a, b]), a map ({X: 1}), or a string
// that looks like a number ("'0644'"). A value keeps any = it holds,
// since only the first one ends the key.
func parseAdhocParams(tokens []string) (map[string]any, error) {
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
			params[key] = parsePropertyValue(raw)
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

// adhocRunbook is the runbook an ad-hoc run carries out: one task calling
// method on hosts with params, written as YAML by an encoder, never by
// joining strings, so no value can change the runbook's shape.
//
// A method named as one of a task's own keys (name, when, block and the
// rest) would be read as that key rather than as a method, so it is
// refused here; every other name is left to the runbook parser and
// validation, which refuse it exactly as they would in a file.
func adhocRunbook(hosts, method string, params map[string]any) ([]byte, error) {
	switch {
	case hosts == "":
		return nil, errors.New("adhoc needs a device or a tag to run on")
	case method == "":
		return nil, errors.New("adhoc needs a method to run")
	case engine.ReservedTaskKeys[method] || method == "import_tasks":
		return nil, fmt.Errorf("%q is a task keyword, not a method; name a method, like exec.command", method)
	}
	task := map[string]any{"name": method, method: nil}
	if len(params) > 0 {
		task[method] = params
	}
	return yaml.Marshal(struct {
		ID    string           `yaml:"id"`
		Name  string           `yaml:"name"`
		Hosts string           `yaml:"hosts"`
		Tasks []map[string]any `yaml:"tasks"`
	}{ID: adhocRunbookID, Name: adhocLabel(hosts, method), Hosts: hosts, Tasks: []map[string]any{task}})
}

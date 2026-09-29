// run's --extra-vars: variables a run is started with, readable by a
// runbook's conditions and, from Phase 117a, its rendered task parameters.
package main

import (
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// extraVarsFlag collects run's repeatable --extra-vars (and -e), as Ansible
// spells them: key=value, key:=yaml, or @file.yaml for a file holding a
// mapping.
type extraVarsFlag struct {
	// given keeps each value in the order it was written, so a file and a
	// key=value are merged in that order and a name given twice is caught
	// wherever it appears.
	given []string
}

// String implements flag.Value.
func (e *extraVarsFlag) String() string { return strings.Join(e.given, " ") }

// Set implements flag.Value, recording one --extra-vars value.
func (e *extraVarsFlag) Set(value string) error {
	if value == "" || value == "@" {
		return fmt.Errorf("--extra-vars needs key=value, key:=yaml or @file.yaml")
	}
	e.given = append(e.given, value)
	return nil
}

// variables merges every --extra-vars value into one map, or returns nil
// when none was given. key=value and key:=yaml are read by adhoc's own
// parser, so both commands type a value the same way; @file.yaml must hold
// a mapping whose keys are parameter names. A name given twice, in a file,
// on the command line or across both, is refused rather than resolved, since
// nothing says which one the author meant.
func (e *extraVarsFlag) variables() (map[string]any, error) {
	if len(e.given) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	add := func(vars map[string]any, from string) error {
		for key, value := range vars {
			if _, repeated := out[key]; repeated {
				return fmt.Errorf("--extra-vars sets %s twice (again in %s)", key, from)
			}
			out[key] = value
		}
		return nil
	}
	for _, value := range e.given {
		if path, isFile := strings.CutPrefix(value, "@"); isFile {
			vars, err := readExtraVarsFile(path)
			if err != nil {
				return nil, err
			}
			if err := add(vars, path); err != nil {
				return nil, err
			}
			continue
		}
		vars, err := parseAdhocParams([]string{value}, nil)
		if err != nil {
			return nil, fmt.Errorf("--extra-vars: %w", err)
		}
		if err := add(vars, "the command line"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// readExtraVarsFile reads path as a YAML mapping of variable names to
// values.
func readExtraVarsFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- the path the operator typed after --extra-vars @, read with their own permissions, exactly as the runbook path argument is
	if err != nil {
		return nil, fmt.Errorf("--extra-vars @%s: %w", path, err)
	}
	var vars map[string]any
	if err := yaml.Unmarshal(raw, &vars); err != nil {
		return nil, fmt.Errorf("--extra-vars @%s is not a YAML mapping: %w", path, err)
	}
	for key := range vars {
		if !paramKey.MatchString(key) {
			return nil, fmt.Errorf("--extra-vars @%s: %q is not a variable name: a name is letters, digits and underscores", path, key)
		}
	}
	return vars, nil
}

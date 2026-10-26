// Package engine: a task's own timeout, Ansible's timeout keyword: how
// long one task may run on one device before it is stopped and fails.
package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// MaxTaskTimeout is the longest timeout a task may set, in seconds: a day,
// the same bound the runbook launch kind's own timeout field has.
const MaxTaskTimeout = 86400

// TimeoutSeconds is a task's timeout key: a whole number of seconds, as
// Ansible's timeout is (1800), or a duration of whole seconds (150s, 30m,
// 1h30m), as adhoc's --timeout takes. Its decoders refuse anything else by
// name, so a runbook is told what the key takes, rather than how YAML failed
// to fit a string into a number.
type TimeoutSeconds int

// timeoutTakes is what every refusal of a timeout's value says it takes.
const timeoutTakes = "a timeout is a whole number of seconds (1800) or a duration of whole seconds (150s, 30m, 1h30m)"

// ParseSeconds reads a number of seconds written as a whole number (40) or
// a Go duration of whole seconds (40s, 5m), the one reading a task's
// timeout key and adhoc's --timeout share. A fraction of a second is
// refused: no timeout here holds one.
func ParseSeconds(text string) (int, error) {
	text = strings.TrimSpace(text)
	if n, err := strconv.Atoi(text); err == nil {
		return n, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%q is neither whole seconds nor a duration such as 40s or 5m", text)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("%q is not a whole number of seconds", text)
	}
	return int(d / time.Second), nil
}

// UnmarshalYAML accepts a YAML integer and refuses everything else with
// what the key takes.
func (t *TimeoutSeconds) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: timeout is a YAML %s; %s", node.Line, yamlKindName(node.Kind), timeoutTakes)
	}
	// A quoted "90" is accepted too, as Ansible converts one.
	n, err := ParseSeconds(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: timeout %q; %s", node.Line, node.Value, timeoutTakes)
	}
	*t = TimeoutSeconds(n)
	return nil
}

// UnmarshalJSON accepts a JSON integer, or a string holding one, and
// refuses everything else with what the key takes, exactly as UnmarshalYAML
// does.
func (t *TimeoutSeconds) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*t = TimeoutSeconds(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if n, err := ParseSeconds(s); err == nil {
			*t = TimeoutSeconds(n)
			return nil
		}
	}
	return fmt.Errorf("timeout %s; %s", string(data), timeoutTakes)
}

// taskLimit is how long one task may run on one device: the smaller of the
// run's bound (WithTaskTimeout) and the task's own timeout, each counting
// only when set, or zero for no bound at all.
func taskLimit(run time.Duration, own TimeoutSeconds) time.Duration {
	task := time.Duration(own) * time.Second
	switch {
	case task <= 0:
		return max(run, 0)
	case run <= 0:
		return task
	}
	return min(run, task)
}

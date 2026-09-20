// Package engine: the check_mode key a runbook, a block or a task may
// carry, and the plan-time answer to whether a task can be checked.
package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// CheckModeFlag is the check_mode key, Ansible's own spelling, on a
// runbook, a block or a task. Set, it runs everything it covers in check
// mode, whatever mode the run itself has: a runbook-level key makes the
// whole run a check, a block-level key covers the block's own tasks and
// its rescue and always tasks, and a task-level key covers that task.
//
// It can only narrow. Ansible's check_mode: false runs a task for real
// inside a check, which breaks the one promise a check makes (nothing
// changes), so it is refused when the runbook is read, as is any value
// that could turn out false later, such as a template. That makes true
// the only value this type ever holds when set. Ansible's other spellings
// of true (yes, on, y, t, in any case) are accepted, since accepting more
// ways to ask for a check cannot widen what a run does.
type CheckModeFlag bool

// checkModeTrue and checkModeFalse are Ansible's spellings of each value,
// lower-cased. YAML 1.2, which yaml.v3 follows, reads only true and false
// as booleans, so a playbook's yes and no arrive here as strings.
var (
	checkModeTrue  = []string{"true", "yes", "on", "y", "t"}
	checkModeFalse = []string{"false", "no", "off", "n", "f", "0"}
)

// checkModeFalseRefusal is why check_mode: false is refused, in words a
// runbook author can act on.
const checkModeFalseRefusal = "check_mode: false is refused: it would run this task for real inside a check, " +
	"and a check must change nothing. A read-only step that should also run during a check " +
	"needs a method that declares check support"

// UnmarshalYAML accepts a YAML boolean true or one of Ansible's string
// spellings of true, and refuses everything else with its reason.
func (c *CheckModeFlag) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("check_mode must be true, got a YAML %s", yamlKindName(node.Kind))
	}
	return c.set(node.Value)
}

// UnmarshalJSON accepts JSON true or one of Ansible's string spellings of
// true, and refuses everything else with its reason, exactly as
// UnmarshalYAML does.
func (c *CheckModeFlag) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("check_mode: %w", err)
	}
	switch v := value.(type) {
	case bool:
		return c.set(fmt.Sprint(v))
	case string:
		return c.set(v)
	default:
		return fmt.Errorf("check_mode must be true, got %s", string(data))
	}
}

// set records raw, one scalar's text, or refuses it.
func (c *CheckModeFlag) set(raw string) error {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case slices.Contains(checkModeTrue, value):
		*c = true
		return nil
	case slices.Contains(checkModeFalse, value):
		return fmt.Errorf("%s", checkModeFalseRefusal)
	case strings.Contains(raw, "{{") || strings.Contains(raw, "{%"):
		return fmt.Errorf("check_mode must be a literal true, not a template (%q): a template could resolve to false when the run starts", raw)
	default:
		return fmt.Errorf("check_mode must be true (or Ansible's yes, on, y or t), got %q", raw)
	}
}

// yamlKindName names a YAML node kind for an error message.
func yamlKindName(k yaml.Kind) string {
	switch k {
	case yaml.SequenceNode:
		return "list"
	case yaml.MappingNode:
		return "map"
	case yaml.AliasNode:
		return "alias"
	default:
		return "document"
	}
}

// propagateCheckMode pushes every check_mode key down to the tasks it
// covers, so each node the executor visits carries its own answer: a
// runbook-level key to every task, and a block's (or an import_tasks
// task's, which resolveImportTasks has already turned into a block) to
// its block, rescue and always tasks, and a parallel group's to its
// children, at any depth. It must run after resolveImportTasks, and
// before anything reads Task.CheckMode.
func propagateCheckMode(def *WorkflowDef) {
	for _, list := range [][]Task{def.PreTasks, def.Tasks, def.PostTasks} {
		propagateCheckModeInto(list, bool(def.CheckMode))
	}
}

// propagateCheckModeInto sets check_mode on each task in tasks when
// inherited is true, then recurses with each task's own answer.
func propagateCheckModeInto(tasks []Task, inherited bool) {
	for i := range tasks {
		t := &tasks[i]
		if inherited {
			t.CheckMode = true
		}
		for _, sub := range [][]Task{t.Block, t.Rescue, t.Always, t.Parallel} {
			propagateCheckModeInto(sub, bool(t.CheckMode))
		}
	}
}

// TaskMode is the mode task runs in when a run of dag starts in runMode:
// a check when the runbook or the task carries check_mode (the builder
// has already copied a block's key onto its tasks), and runMode
// otherwise. Only a real run is narrowed; a check stays a check and an
// unknown mode keeps its refusal. The empty mode is a real run, as
// validation's zero WorldView.Mode is. dag or task may be nil.
//
// It is the one statement of this rule. The executor decides what runs
// with it and validation decides what to refuse with it, so the two
// cannot disagree about which tasks are only checked (the shape of
// FAILURE_PATTERNS 249).
func TaskMode(runMode collection.Mode, dag *DAG, task *Task) collection.Mode {
	narrowed := (dag != nil && dag.CheckMode) || (task != nil && bool(task.CheckMode))
	if narrowed && (runMode == collection.ModeExecute || runMode == "") {
		return collection.ModeCheck
	}
	return runMode
}

// builtinCheckable is every built-in action whose Check is its own
// Execute, because it runs in process and changes nothing on any device
// (builtinActionExecutor.Check). The transport actions (ssh_exec and its
// kin) run arbitrary commands and cannot be checked.
var builtinCheckable = map[string]bool{
	"noop":                          true,
	"set_metadata":                  true,
	"pleiades.builtin.set_metadata": true,
}

// Checkable reports whether a task calling fqcn with params can run in
// check mode, and says why not when it cannot. It is the plan-time
// statement of what the executor does at run time: a built-in in
// builtinCheckable checks by executing, a Collection method checks
// through its declared Check, and everything else is reported unchecked.
// A method whose check covers only some calls answers for these params
// through its CheckCall, the same function its Check asks, so a call it
// would only ever report unchecked is caught here too. Validation uses
// it to refuse check_mode on a task that could never honor it.
//
// params are the task's own, exactly as its method receives them: the
// engine renders nothing into a Collection method's parameters, so the
// answer here is the answer the run would give.
func Checkable(fqcn string, params map[string]any) (bool, string) {
	if builtinCheckable[fqcn] {
		return true, ""
	}
	if d, ok := collection.Lookup(fqcn); ok {
		if !d.Manifest.SupportsCheck {
			if d.Manifest.NoCheckReason != "" {
				return false, fmt.Sprintf("%s declares no check support: %s", fqcn, d.Manifest.NoCheckReason)
			}
			return false, fmt.Sprintf("%s declares no check support", fqcn)
		}
		if d.CheckCall == nil {
			return true, ""
		}
		if err := d.CheckCall(params); err != nil {
			reason := err.Error()
			var cannot *collection.CannotCheckError
			if errors.As(err, &cannot) {
				reason = cannot.Reason
			}
			return false, fmt.Sprintf("%s cannot check this call: %s", fqcn, reason)
		}
		return true, ""
	}
	return false, fmt.Sprintf("%s is neither a Collection method with check support nor one of the built-in actions that can be checked (%s)",
		fqcn, strings.Join(sortedKeys(builtinCheckable), ", "))
}

// RunbookKeys is every key a runbook's top level may carry: WorkflowDef's
// own YAML and JSON key names. A runbook carrying any other key is
// refused, naming it (checkRunbookKeys), because a decoder that drops an
// unknown key turns a safety keyword it does not support into a silent
// no-op: a runbook-level check_mode: true used to be accepted and ignored,
// and the runbook ran for real (FAILURE_PATTERNS 252). Exported so the
// documentation generator checks its descriptions against this exact
// set, and a test checks this set against WorkflowDef's struct tags.
var RunbookKeys = map[string]bool{
	"id":         true,
	"name":       true,
	"hosts":      true,
	"type":       true,
	"metadata":   true,
	"check_mode": true,
	"pretasks":   true,
	"tasks":      true,
	"posttasks":  true,
}

// checkRunbookKeys refuses any top-level key not in RunbookKeys, naming
// each one and, when one known key is close, suggesting it.
func checkRunbookKeys(keys []string) error {
	var unknown []string
	for _, k := range keys {
		if !RunbookKeys[k] {
			if near := nearestKey(k, RunbookKeys); near != "" {
				unknown = append(unknown, fmt.Sprintf("%q (did you mean %q?)", k, near))
			} else {
				unknown = append(unknown, fmt.Sprintf("%q", k))
			}
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("unknown top-level runbook key %s; a runbook's top level may carry only %s",
		strings.Join(unknown, ", "), strings.Join(sortedKeys(RunbookKeys), ", "))
}

// nearestKey returns the key in known closest to k by edit distance, when
// it is close enough to be a plausible typo (at most two edits, and fewer
// than half of k's length), and "" otherwise.
func nearestKey(k string, known map[string]bool) string {
	best, bestDist := "", 3
	for _, candidate := range sortedKeys(known) {
		if d := editDistance(k, candidate); d < bestDist && d*2 < len(k) {
			best, bestDist = candidate, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// sortedKeys returns m's keys in sorted order, for stable messages.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

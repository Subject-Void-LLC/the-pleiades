package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// taskKeyDoc is one task-level key's hand-written description, paired at
// generation time against engine.ReservedTaskKeys, the parser's own
// authoritative list. taskKeyDescriptions and ReservedTaskKeys are
// checked for an exact match (see generateTaskKeys): a key here the
// parser no longer recognizes, or a key the parser recognizes with no
// description here, both fail the build rather than silently drift.
type taskKeyDoc struct {
	Key         string
	Description string
}

var taskKeyDescriptions = []taskKeyDoc{
	{"name", "Free-form label. Printed in the plan and in per-task run output; no uniqueness requirement."},
	{"fqcn", "The action this task performs: a bare engine keyword (`noop`) or a namespaced Collection method (`pkg.apt.install`)."},
	{"params", "Arbitrary map passed to the action named by `fqcn`. Never templated: a literal value, with no `{{ }}` rendering of any kind."},
	{"register", "Names this task's result so a later task's `when_cel` can read it as `stat.<name>[<deviceID>].<field>`."},
	{"when", "Ansible-compatible conditional: one boolean expression, or a list of them ANDed together."},
	{"when_or", "Like `when`, but a list is ORed instead of ANDed. Has no Ansible equivalent."},
	{"when_cel", "One raw CEL expression, for a condition `when`/`when_or` cannot express. Exactly one of `when`/`when_or`/`when_cel` may be set."},
	{"register_mask", "Masks a field (or a dotted nested path) of this task's own registered result the instant it registers, before anything downstream can see it unmasked."},
	{"secret_mask", "`{register, fields}`: masks a named, already-registered result's top-level fields. Unlike `register_mask`, does not support dotted paths."},
	{"lock_acquisition", "`per_device_as_reached` (default) or `all_at_plan_time`: when this task's device lock is acquired relative to the rest of the run."},
	{"block", "Groups tasks so `rescue`/`always` can catch or clean up after a failure among them, mirroring Ansible's own block/rescue/always."},
	{"rescue", "Tasks run if any task in the sibling `block` fails. Only meaningful alongside `block`."},
	{"always", "Tasks run after the sibling `block`, whether or not it failed. Only meaningful alongside `block`."},
	{"parallel", "Native fan-out/join: runs its child tasks concurrently. Mutually exclusive with `fqcn` and `block`; may not carry `rescue`/`always`."},
}

// runbookKeyDescriptions documents WorkflowDef's own top-level keys
// (internal/engine/dag.go). Unlike the task-level keys, there is no
// exported map to check this list against: WorkflowDef's shape comes
// from Go struct tags, not a validation-time lookup table. Kept here by
// hand, and the seven keys named are cross-checked by eye against
// dag.go's own struct definition each time this file is touched.
var runbookKeyDescriptions = []taskKeyDoc{
	{"id", "The runbook's own identifier. Required. Restricted to `[A-Za-z0-9_-]`, since it is embedded into a NATS subject."},
	{"hosts", "Default target for every task that does not set its own. A task's own `params.target` (or module-as-key sugar's bare `target:`) still wins when set."},
	{"type", "Runbook-type discriminator. `native` (the default) or the empty string; `ansible` is reserved and non-actionable today."},
	{"metadata", "Runbook-level metadata. Its only field today is `service_effecting`; blast radius itself is always computed, never authored."},
	{"pretasks", "Tasks that run before `tasks`. Optional."},
	{"tasks", "The runbook's main task list. Required."},
	{"posttasks", "Tasks that run after `tasks`. Optional."},
}

// generateTaskKeys emits outDir/task-keys.md: the runbook- and
// task-level key reference, task-level keys checked against
// engine.ReservedTaskKeys so this page cannot list a key the parser does
// not accept, or omit one it does.
func generateTaskKeys(outDir string) error {
	if err := checkTaskKeysComplete(); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Runbook and task keys\n\n")
	b.WriteString("Every key a runbook author can write, generated against the parser's own accepted key " +
		"list so this page cannot drift from what actually validates.\n\n")

	b.WriteString("## Runbook scope\n\n")
	b.WriteString(taskKeyTable(runbookKeyDescriptions))

	b.WriteString("\n## Task scope\n\n")
	b.WriteString(taskKeyTable(taskKeyDescriptions))

	return os.WriteFile(filepath.Join(outDir, "task-keys.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}

func taskKeyTable(keys []taskKeyDoc) string {
	rows := make([][]string, len(keys))
	for i, k := range keys {
		rows[i] = []string{code(k.Key), k.Description}
	}
	return table([]string{"Key", "Description"}, rows)
}

// checkTaskKeysComplete proves taskKeyDescriptions and
// engine.ReservedTaskKeys name exactly the same set, in both directions.
func checkTaskKeysComplete() error {
	documented := map[string]bool{}
	for _, k := range taskKeyDescriptions {
		documented[k.Key] = true
	}

	var undocumented, stale []string
	for key := range engine.ReservedTaskKeys {
		if !documented[key] {
			undocumented = append(undocumented, key)
		}
	}
	for key := range documented {
		if !engine.ReservedTaskKeys[key] {
			stale = append(stale, key)
		}
	}

	if len(undocumented) > 0 {
		return fmt.Errorf("gendocs: task key(s) the parser accepts but this page does not document: %s", strings.Join(undocumented, ", "))
	}
	if len(stale) > 0 {
		return fmt.Errorf("gendocs: task key(s) this page documents but the parser no longer accepts: %s", strings.Join(stale, ", "))
	}
	return nil
}

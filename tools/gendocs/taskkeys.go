package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// taskKeyDoc is one task-level key's hand-written description, paired at
// generation time against engine.ReservedTaskKeys, the parser's own
// authoritative list. taskKeyDescriptions and ReservedTaskKeys are
// checked for an exact match (see generateTaskKeys): a key here the
// parser no longer recognizes, or a key the parser recognizes with no
// description here, both fail the build rather than silently drift.
//
// That pairing proves the key NAMES match, and nothing more. No check
// compares a Description against what the engine does with the key, so a
// description can confidently state behavior that does not exist: the
// rescue and always rows below were wrong for exactly that reason.
// Changing a Description is a documentation claim, and it has to be
// verified by hand against the code that reads the key.
type taskKeyDoc struct {
	Key         string
	Description string
}

var taskKeyDescriptions = []taskKeyDoc{
	{"name", "Free-form label. Printed in the plan and in per-task run output; no uniqueness requirement."},
	{"fqcn", "The legacy way to name a task's action, with its parameters under `params`: `pleiades validate` warns on it (rule `task-form`). Write the action as the task's key instead, with its parameters as the mapping under it: `pkg.apt.install: {name: curl}`, or a bare engine keyword such as `noop: {}`."},
	{"params", "The legacy form's parameters (see `fqcn`); a task written with its method as the key carries them as the mapping under that key, and everything here applies to them alike. A string holding `{{ }}` renders when the task runs, through the platform's one template renderer, and may read three things: `vars` (the run's variables: `pleiades run --extra-vars`, a launch's extra variables and survey answers, never a credential's secret), `nodes` (every earlier registered result, as `nodes.<register>[<deviceID>]`) and `result` (a register exactly one device or one device-less task wrote, as `result.<register>`). A value that is one expression and nothing else keeps its type, so `\"{{ result.ticket.json.tags }}\"` hands the method a list, except that a parameter the method declares as text (or a list of text) receives the text of what renders, so `\"{{ vars.n }}\"` is `7`, not the number; a rendered value of a type the method does not declare for that parameter, such as a list into text or a word into a number, fails the task naming the parameter. A literal value is held to its declared type by `pleiades validate` (rule `param-type`), and nothing is converted: quote `\"0644\"`, since an unquoted `0644` is a number. A parameter the method marks required must be given, by its name or an alias the method declares (rule `param-required`); null does not count. A parameter a method marks as command text accepts an expression only when it ends with `| quote` (a shell) or `| cli_token` (a network CLI), and a URL parameter must name its scheme and host before its first expression; both are checked by `pleiades validate` and again when the task runs. A value with no `{{` passes through untouched."},
	{"register", "Names this task's result so a later task can read it: a condition as `stat.<name>[<deviceID>].<field>`, and rendered params as `nodes.<name>[<deviceID>]`; when one device (or one device-less task) wrote it, both read it as `result.<name>.<field>`, with no device id."},
	{"within", "The device name or inventory tag a target rendered from data must stay inside: `params.target: \"{{ result.ticket.json.ci }}\"` with `within: core-switches` runs on the device the ticket names, and fails, naming both, when that device is not in `core-switches`. The rendered target names devices only (one, several separated by commas, or a list), never a tag, and nothing falls back to `hosts:`. Required beside a target holding `{{ }}`, refused anywhere else, written literally. `pleiades validate` checks capabilities against every device `within:` names. It does not stop a ticket naming a wrong but allowed device."},
	{"check_mode", "Ansible's `check_mode`. `true` (or `yes`/`on`) runs this task in check mode even in a real run: its method's check runs instead of the real call, its result is a prediction printed as \"would change\", and it is not journaled. On a `block` it covers the block's own tasks and its `rescue` and `always` tasks; on an `import_tasks` task, every imported task. `false`, or anything that could turn out false such as a template, is refused, because it would run a task for real inside a check. Validation refuses it on an action that cannot be checked, and refuses a task that runs for real whose condition reads a checked task's registered result."},
	{"timeout", "Ansible's `timeout`: how many whole seconds this task may run on each device before it is stopped and fails, saying its timeout ran out. From 1 to 86400 (a day); `0`, or leaving it out, sets no bound of its own. It applies afresh to each device, and when the run has a bound too (a launch's `timeout` field) the smaller wins. Stopping a task ends the method's work where its connection allows: an SSH command is sent `SIGTERM` and its session closed, though processes that command started are not signalled. Only on a method call: refused on a `block` or `parallel` group, whose tasks each set their own, and in a `rollback` step."},
	{"tags", "Ansible's `tags`: names `pleiades run --tags` and `--skip-tags` select this task by, as a list or one comma-separated string. On a `block` or `parallel` group they pass down to every task inside, and a runbook's own top-level `tags` pass down to every task. Selection follows Ansible exactly: a task tagged `always` runs whatever `--tags` says, a task tagged `never` runs only when a run names one of its tags (so by default it does not run at all), and `all`, `tagged` and `untagged` mean something only in a filter, so a task may not carry them. A template is refused, since nothing renders a tag."},
	{"when", "Ansible-compatible conditional: one boolean expression, or a list of them ANDed together. Any of the three condition keys may call a registered [filter](filters/index.md) as part of the expression."},
	{"when_or", "Like `when`, but a list is ORed instead of ANDed. Has no Ansible equivalent."},
	{"when_cel", "One raw CEL expression, for a condition `when`/`when_or` cannot express. Exactly one of `when`/`when_or`/`when_cel` may be set. See the [filter reference](filters/index.md) for the functions callable from here, beyond CEL's own operators."},
	{"register_mask", "Masks a field (or a dotted nested path) of this task's own registered result the instant it registers, before anything downstream can see it unmasked."},
	{"secret_mask", "`{register, fields}`: masks a named, already-registered result's top-level fields. Unlike `register_mask`, does not support dotted paths."},
	{"lock_acquisition", "`per_device_as_reached` (default) or `all_at_plan_time`: when this task's device lock is acquired relative to the rest of the run."},
	{"block", "Groups its child tasks into one ordered sub-list, mirroring Ansible's own `block:`. The children run in order, in the position the block itself occupies, followed by its `rescue` and `always` tasks: see the two rows below. A `when`, `secret_mask` or `timeout` on the block itself is refused; put it on the tasks inside."},
	{"rescue", "Tasks that run on a device where a task of the block failed, mirroring Ansible's `rescue:`. The block's remaining tasks do not run on that device; other devices carry on with the block. A rescue that runs to its end without failing answers the failure: the run goes on, its outcome is `rescued` rather than `complete` (exit 0, or 4 with `pleiades run --fail-on-rescue`), and the failed task reports `rescued_by`. A rescue task reads the failure it answers as `error` (`error.task`, `error.name`, `error.method`, `error.device`, `error.message`, `error.unreached`), which `pleiades validate` refuses anywhere else. A failure held for a person's decision is not rescued. In a check a rescue task is not predicted (`conditional_skip`), and does not make the check incomplete. A block one of whose tasks runs somewhere other than the runbook's `hosts:` (a target of its own, or a method that uses no device) runs as one unit: a failure on any device fails it on every device, the rest of it is skipped everywhere, and its rescue and always tasks run once each on their own targets, a rescue reading the first failure as `error`; otherwise each device is handled apart. A register written only by the block's own tasks may not exist after a rescue, so `pleiades validate` refuses a later task that reads it unless the rescue registers it too or the read handles absence (`| default(...)`, `has(...)`)."},
	{"always", "Tasks that run on every device the block ran on, after the block and any rescue, whether the block succeeded, failed or was rescued, mirroring Ansible's `always:`. A failure the block did not answer still ends the run, after `always` has run; a run that has to end for any reason while inside a block still runs that block's `always` on every device first, and nothing else. A failure inside `always` skips the rest of it on that device. In a check, `always` is predicted on the assumption that the block succeeded."},
	{"parallel", "Native fan-out/join: runs its child tasks concurrently. Mutually exclusive with `fqcn` and `block`; may not carry `rescue`/`always`."},
	{"rollback", "This task's authored undo: a list of method calls, written like any task (the method as the key), that `pleiades rollback` runs on each device this task changed, in place of the undo the method recorded. Use it where the recorded undo cannot serve: a method that records none (`exec.command`), one whose undo keeps a value out of the journal (a file's prior content), or when you want a different undo. An empty list, `rollback: []`, says undoing the task means doing nothing (a command that only read, or a model call that changed no device): a rollback runs nothing for it and reports it as nothing to undo, and validate's `reversible` and `rollback_on_failure` rules take it as an answer. Allowed only on a method call (one inside a `rescue` or `always` included), not on a `block`, `parallel` or `import_tasks` task. A block's `rollback_on_failure` runs the same steps when the block fails. Each step is a method and its params only: no `target` (it runs on the device this task changed), `register`, `when`, `check_mode` or `tags`, since a rollback runs long after this run and has none of its results. Adding or changing it does not change the runbook's version, so an undo written after a run failed still matches that run."},
	{"rollback_on_failure", "`true` on a `block`: when one of the block's own tasks fails on a device, what the block's tasks completed there is undone first, newest first, before the block's `rescue` starts, so the rescue starts from where the block began. Each undo is what `pleiades rollback` would run (the task's `rollback` list, or the undo its method recorded) and is journaled as a rollback of that task, so a later `pleiades rollback` of the run does not repeat it; in the run's report it is a task of its own whose `undoes` names the task it undid. The task that failed is not undone, since a failed task records no undo. `pleiades validate` refuses the key on a block holding a task it could not undo exactly (rule `rollback_on_failure`), the test `reversible: true` applies. If an undo fails, the rescue does not run, `always` does, and the run fails with both failures. With no `rescue` the undo still runs, then the failure goes on as it would have. A block that runs as one unit is undone on every device. A check never undoes. Unlike `rollback`, it changes the runbook's version, since it changes what the run does."},
}

// runbookKeyDescriptions documents WorkflowDef's own top-level keys
// (internal/engine/dag.go), checked at generation time against
// engine.RunbookKeys, the parser's list of accepted top-level keys, which
// a test in internal/engine keeps equal to WorkflowDef's struct tags.
var runbookKeyDescriptions = []taskKeyDoc{
	{"id", "The runbook's own identifier. Required. Restricted to `[A-Za-z0-9_-]`, since it is embedded into a NATS subject."},
	{"name", "The runbook's human title, like an Ansible play's `name`. Optional: a listing shows `id` when it is empty."},
	{"check_mode", "Ansible's `check_mode`. `true` (or `yes`/`on`) makes the whole run a check, exactly as `pleiades run --mode check` does. `false` is refused. Any top-level key not on this page is refused too, naming it, rather than silently ignored."},
	{"tags", "Ansible's play-level `tags`: every task in the runbook carries them too, as if written on each one. See `tags` under task keys for how a run selects by them."},
	{"hosts", "Default target for every task that does not set its own. A task's own `params.target` (or module-as-key sugar's bare `target:`) still wins when set."},
	{"type", "Runbook-type discriminator. `native` (the default) or the empty string; `ansible` is reserved and non-actionable today."},
	{"metadata", "Runbook-level metadata. `service_effecting` marks a run as affecting live service, as opposed to purely read-only or diagnostic; blast radius itself is always computed, never authored. `interruptible` (default true when omitted) marks whether a Runner that loses its heartbeat with the Controller may safely self-abort this runbook before the Controller's own lock TTL expires; set it `false` for a task that must finish once started."},
	{"pretasks", "Tasks that run before `tasks`. Optional."},
	{"tasks", "The runbook's main task list. Required."},
	{"posttasks", "Tasks that run after `tasks`. Optional."},
	{"reversible", "`true` promises that `pleiades rollback` can undo every task that changes a device: each one calls a method whose recorded undo replays whole, calls a read-only method, or has its own `rollback` list. Validation refuses the runbook otherwise, naming each task and why, before anything runs. Optional; it does not change the runbook's version."},
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
	b.WriteString("Every key a runbook author can write. The key list itself is generated from the " +
		"parser's own list of accepted keys, so this page cannot name a key that fails validation, or " +
		"leave out one that passes. That check covers key names only. Each description below is " +
		"hand-written, and nothing verifies a description against the engine.\n\n")

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
// engine.ReservedTaskKeys name exactly the same set, in both directions,
// and runbookKeyDescriptions and engine.RunbookKeys likewise.
func checkTaskKeysComplete() error {
	if err := sameKeys("task", taskKeyDescriptions, engine.ReservedTaskKeys); err != nil {
		return err
	}
	return sameKeys("runbook", runbookKeyDescriptions, engine.RunbookKeys)
}

// sameKeys reports any key accepted but undocumented, or documented but
// no longer accepted, naming the scope in the error.
func sameKeys(scope string, docs []taskKeyDoc, accepted map[string]bool) error {
	documented := map[string]bool{}
	for _, k := range docs {
		documented[k.Key] = true
	}

	var undocumented, stale []string
	for key := range accepted {
		if !documented[key] {
			undocumented = append(undocumented, key)
		}
	}
	for key := range documented {
		if !accepted[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(stale)

	if len(undocumented) > 0 {
		return fmt.Errorf("gendocs: %s key(s) the parser accepts but this page does not document: %s", scope, strings.Join(undocumented, ", "))
	}
	if len(stale) > 0 {
		return fmt.Errorf("gendocs: %s key(s) this page documents but the parser no longer accepts: %s", scope, strings.Join(stale, ", "))
	}
	return nil
}

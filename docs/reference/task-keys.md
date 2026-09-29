---
status: beta
---

# Runbook and task keys

Every key a runbook author can write. The key list itself is generated from the parser's own list of accepted keys, so this page cannot name a key that fails validation, or leave out one that passes. That check covers key names only. Each description below is hand-written, and nothing verifies a description against the engine, so a key that validates does not always do something: `rescue` and `always` validate and never run.

## Runbook scope

| Key | Description |
| --- | --- |
| `id` | The runbook's own identifier. Required. Restricted to `[A-Za-z0-9_-]`, since it is embedded into a NATS subject. |
| `name` | The runbook's human title, like an Ansible play's `name`. Optional: a listing shows `id` when it is empty. |
| `check_mode` | Ansible's `check_mode`. `true` (or `yes`/`on`) makes the whole run a check, exactly as `pleiades run --mode check` does. `false` is refused. Any top-level key not on this page is refused too, naming it, rather than silently ignored. |
| `tags` | Ansible's play-level `tags`: every task in the runbook carries them too, as if written on each one. See `tags` under task keys for how a run selects by them. |
| `hosts` | Default target for every task that does not set its own. A task's own `params.target` (or module-as-key sugar's bare `target:`) still wins when set. |
| `type` | Runbook-type discriminator. `native` (the default) or the empty string; `ansible` is reserved and non-actionable today. |
| `metadata` | Runbook-level metadata. `service_effecting` marks a run as affecting live service, as opposed to purely read-only or diagnostic; blast radius itself is always computed, never authored. `interruptible` (default true when omitted) marks whether a Runner that loses its heartbeat with the Controller may safely self-abort this runbook before the Controller's own lock TTL expires; set it `false` for a task that must finish once started. |
| `pretasks` | Tasks that run before `tasks`. Optional. |
| `tasks` | The runbook's main task list. Required. |
| `posttasks` | Tasks that run after `tasks`. Optional. |
| `reversible` | `true` promises that `pleiades rollback` can undo every task that changes a device: each one calls a method whose recorded undo replays whole, calls a read-only method, or has its own `rollback` list. Validation refuses the runbook otherwise, naming each task and why, before anything runs. Optional; it does not change the runbook's version. |

## Task scope

| Key | Description |
| --- | --- |
| `name` | Free-form label. Printed in the plan and in per-task run output; no uniqueness requirement. |
| `fqcn` | The action this task performs: a bare engine keyword (`noop`) or a namespaced Collection method (`pkg.apt.install`). |
| `params` | Arbitrary map passed to the action named by `fqcn`. A string holding `{{ }}` renders when the task runs, through the platform's one template renderer, and may read three things: `vars` (the run's variables: `pleiades run --extra-vars`, a launch's extra variables and survey answers, never a credential's secret), `nodes` (every earlier registered result, as `nodes.<register>[<deviceID>]`) and `result` (a register exactly one device or one device-less task wrote, as `result.<register>`). A value that is one expression and nothing else keeps its type, so `"{{ result.ticket.json.tags }}"` hands the method a list. A parameter a method marks as command text accepts an expression only when it ends with `\| quote` (a shell) or `\| cli_token` (a network CLI), and a URL parameter must name its scheme and host before its first expression; both are checked by `pleiades validate` and again when the task runs. A value with no `{{` passes through untouched. |
| `register` | Names this task's result so a later task can read it: a condition as `stat.<name>[<deviceID>].<field>`, and rendered params as `nodes.<name>[<deviceID>]`; when one device (or one device-less task) wrote it, both read it as `result.<name>.<field>`, with no device id. |
| `within` | The device name or inventory tag a target rendered from data must stay inside: `params.target: "{{ result.ticket.json.ci }}"` with `within: core-switches` runs on the device the ticket names, and fails, naming both, when that device is not in `core-switches`. The rendered target names devices only (one, several separated by commas, or a list), never a tag, and nothing falls back to `hosts:`. Required beside a target holding `{{ }}`, refused anywhere else, written literally. `pleiades validate` checks capabilities against every device `within:` names. It does not stop a ticket naming a wrong but allowed device. |
| `check_mode` | Ansible's `check_mode`. `true` (or `yes`/`on`) runs this task in check mode even in a real run: its method's check runs instead of the real call, its result is a prediction printed as "would change", and it is not journaled. On a `block` it covers the block's own tasks and its `rescue` and `always` tasks; on an `import_tasks` task, every imported task. `false`, or anything that could turn out false such as a template, is refused, because it would run a task for real inside a check. Validation refuses it on an action that cannot be checked, and refuses a task that runs for real whose condition reads a checked task's registered result. |
| `tags` | Ansible's `tags`: names `pleiades run --tags` and `--skip-tags` select this task by, as a list or one comma-separated string. On a `block` or `parallel` group they pass down to every task inside, and a runbook's own top-level `tags` pass down to every task. Selection follows Ansible exactly: a task tagged `always` runs whatever `--tags` says, a task tagged `never` runs only when a run names one of its tags (so by default it does not run at all), and `all`, `tagged` and `untagged` mean something only in a filter, so a task may not carry them. A template is refused, since nothing renders a tag. |
| `when` | Ansible-compatible conditional: one boolean expression, or a list of them ANDed together. Any of the three condition keys may call a registered [filter](filters/index.md) as part of the expression. |
| `when_or` | Like `when`, but a list is ORed instead of ANDed. Has no Ansible equivalent. |
| `when_cel` | One raw CEL expression, for a condition `when`/`when_or` cannot express. Exactly one of `when`/`when_or`/`when_cel` may be set. See the [filter reference](filters/index.md) for the functions callable from here, beyond CEL's own operators. |
| `register_mask` | Masks a field (or a dotted nested path) of this task's own registered result the instant it registers, before anything downstream can see it unmasked. |
| `secret_mask` | `{register, fields}`: masks a named, already-registered result's top-level fields. Unlike `register_mask`, does not support dotted paths. |
| `lock_acquisition` | `per_device_as_reached` (default) or `all_at_plan_time`: when this task's device lock is acquired relative to the rest of the run. |
| `block` | Groups its child tasks into one ordered sub-list, mirroring Ansible's own `block:`. The children run in order, in the position the block itself occupies. Its `rescue`/`always` siblings are accepted but never run: see the two rows below. |
| `rescue` | **Accepted, validated, printed in the plan, and never executed.** The builder registers rescue tasks as graph nodes but wires no edges to them (`internal/engine/tasktree.go`), and the executor follows edges only (`internal/engine/executor.go`), so a `block` whose child fails runs no rescue handler: the run just fails. Do not rely on it to recover from a failure. |
| `always` | **Accepted, validated, printed in the plan, and never executed**, for the same reason as `rescue`, and this is the more dangerous of the two. When the sibling `block` succeeds, the run prints `run complete` and exits 0 while every `always` task is skipped in silence, so nothing tells you the cleanup did not happen. Until this is implemented, put cleanup steps at the end of the `block` itself. |
| `parallel` | Native fan-out/join: runs its child tasks concurrently. Mutually exclusive with `fqcn` and `block`; may not carry `rescue`/`always`. |
| `rollback` | This task's authored undo: a list of method calls, written like any task (the method as the key), that `pleiades rollback` runs on each device this task changed, in place of the undo the method recorded. Use it where the recorded undo cannot serve: a method that records none (`exec.command`), one whose undo keeps a value out of the journal (a file's prior content), or when you want a different undo. Allowed only on a method call, not on a `block`, `parallel`, `import_tasks`, `rescue` or `always` task. Each step is a method and its params only: no `target` (it runs on the device this task changed), `register`, `when`, `check_mode` or `tags`, since a rollback runs long after this run and has none of its results. Adding or changing it does not change the runbook's version, so an undo written after a run failed still matches that run. |

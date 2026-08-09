---
status: beta
---

# Runbook and task keys

Every key a runbook author can write. The key list itself is generated from the parser's own list of accepted keys, so this page cannot name a key that fails validation, or leave out one that passes. That check covers key names only. Each description below is hand-written, and nothing verifies a description against the engine, so a key that validates does not always do something: `rescue` and `always` validate and never run.

## Runbook scope

| Key | Description |
| --- | --- |
| `id` | The runbook's own identifier. Required. Restricted to `[A-Za-z0-9_-]`, since it is embedded into a NATS subject. |
| `hosts` | Default target for every task that does not set its own. A task's own `params.target` (or module-as-key sugar's bare `target:`) still wins when set. |
| `type` | Runbook-type discriminator. `native` (the default) or the empty string; `ansible` is reserved and non-actionable today. |
| `metadata` | Runbook-level metadata. `service_effecting` marks a run as affecting live service, as opposed to purely read-only or diagnostic; blast radius itself is always computed, never authored. `interruptible` (default true when omitted) marks whether a Runner that loses its heartbeat with the Controller may safely self-abort this runbook before the Controller's own lock TTL expires; set it `false` for a task that must finish once started. |
| `pretasks` | Tasks that run before `tasks`. Optional. |
| `tasks` | The runbook's main task list. Required. |
| `posttasks` | Tasks that run after `tasks`. Optional. |

## Task scope

| Key | Description |
| --- | --- |
| `name` | Free-form label. Printed in the plan and in per-task run output; no uniqueness requirement. |
| `fqcn` | The action this task performs: a bare engine keyword (`noop`) or a namespaced Collection method (`pkg.apt.install`). |
| `params` | Arbitrary map passed to the action named by `fqcn`. Never templated: a literal value, with no `{{ }}` rendering of any kind. |
| `register` | Names this task's result so a later task's `when_cel` can read it as `stat.<name>[<deviceID>].<field>`. |
| `when` | Ansible-compatible conditional: one boolean expression, or a list of them ANDed together. |
| `when_or` | Like `when`, but a list is ORed instead of ANDed. Has no Ansible equivalent. |
| `when_cel` | One raw CEL expression, for a condition `when`/`when_or` cannot express. Exactly one of `when`/`when_or`/`when_cel` may be set. |
| `register_mask` | Masks a field (or a dotted nested path) of this task's own registered result the instant it registers, before anything downstream can see it unmasked. |
| `secret_mask` | `{register, fields}`: masks a named, already-registered result's top-level fields. Unlike `register_mask`, does not support dotted paths. |
| `lock_acquisition` | `per_device_as_reached` (default) or `all_at_plan_time`: when this task's device lock is acquired relative to the rest of the run. |
| `block` | Groups its child tasks into one ordered sub-list, mirroring Ansible's own `block:`. The children run in order, in the position the block itself occupies. Its `rescue`/`always` siblings are accepted but never run: see the two rows below. |
| `rescue` | **Accepted, validated, printed in the plan, and never executed.** The builder registers rescue tasks as graph nodes but wires no edges to them (`internal/engine/tasktree.go`), and the executor follows edges only (`internal/engine/executor.go`), so a `block` whose child fails runs no rescue handler: the run just fails. Do not rely on it to recover from a failure. |
| `always` | **Accepted, validated, printed in the plan, and never executed**, for the same reason as `rescue`, and this is the more dangerous of the two. When the sibling `block` succeeds, the run prints `run complete` and exits 0 while every `always` task is skipped in silence, so nothing tells you the cleanup did not happen. Until this is implemented, put cleanup steps at the end of the `block` itself. |
| `parallel` | Native fan-out/join: runs its child tasks concurrently. Mutually exclusive with `fqcn` and `block`; may not carry `rescue`/`always`. |

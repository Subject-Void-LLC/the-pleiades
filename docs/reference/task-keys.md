---
status: beta
---

# Runbook and task keys

Every key a runbook author can write, generated against the parser's own accepted key list so this page cannot drift from what actually validates.

## Runbook scope

| Key | Description |
| --- | --- |
| `id` | The runbook's own identifier. Required. Restricted to `[A-Za-z0-9_-]`, since it is embedded into a NATS subject. |
| `hosts` | Default target for every task that does not set its own. A task's own `params.target` (or module-as-key sugar's bare `target:`) still wins when set. |
| `type` | Runbook-type discriminator. `native` (the default) or the empty string; `ansible` is reserved and non-actionable today. |
| `metadata` | Runbook-level metadata. Its only field today is `service_effecting`; blast radius itself is always computed, never authored. |
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
| `block` | Groups tasks so `rescue`/`always` can catch or clean up after a failure among them, mirroring Ansible's own block/rescue/always. |
| `rescue` | Tasks run if any task in the sibling `block` fails. Only meaningful alongside `block`. |
| `always` | Tasks run after the sibling `block`, whether or not it failed. Only meaningful alongside `block`. |
| `parallel` | Native fan-out/join: runs its child tasks concurrently. Mutually exclusive with `fqcn` and `block`; may not carry `rescue`/`always`. |

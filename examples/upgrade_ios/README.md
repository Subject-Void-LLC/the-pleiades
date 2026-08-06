# Upgrading a Cisco IOS-XE switch: Ansible vs. Pleiades

This directory has the same upgrade written twice, so you can compare them line for line:

```
examples/upgrade_ios/
  ansible/
    inventory.ini
    group_vars/catalyst_lab.yml
    upgrade_ios_xe.yml       <- the Ansible playbook
  pleiades/
    inventory.yaml
    runbooks/upgrade_ios_xe.yaml         <- the Pleiades runbook
    runbooks/upgrade_ios_xe_sugar.yaml   <- the same runbook, module-as-key syntax
```

Both upgrade a Catalyst switch named `sw1` from whatever it is running today to IOS-XE
17.03.04: back up the config, copy the new image to flash, verify its checksum, point the
boot variable at it, reload, then confirm the new version came up. This is the same
scenario `docs/cisco_lab_scenario.md` walks through at the architecture level; these two
files are what actually authoring it looks like.

## Task-by-task

| Step | Ansible module | Pleiades fqcn |
|------|-----------------|----------------|
| Get running config (masked) | `no_log: true` (a task attribute, not a module) | `net.cli.command` + `register_mask:` |
| Record current version | `cisco.ios.ios_facts` | `net.cli.command` |
| Check flash free space | `ansible.netcommon.cli_command` | `net.cli.command` |
| Back up running config | `cisco.ios.ios_config` (`backup: true`) | `net.ios.config` (`backup: true`) |
| Copy image to flash | `ansible.netcommon.cli_command` | `net.cli.command` |
| Verify image checksum | `ansible.netcommon.cli_command` | `net.cli.command` |
| Set boot variable, save | `cisco.ios.ios_config` | `net.ios.config` |
| Reload | `ansible.netcommon.cli_command` | `net.cli.command` |
| Wait for the switch to come back | `ansible.builtin.wait_for` | `pleiades.builtin.wait.port` |
| Re-check version | `cisco.ios.ios_facts` | `net.cli.command` |
| Record run report data | `ansible.builtin.set_stats` | `pleiades.builtin.set_metadata` |

The `net.*` names come straight out of `docs/hephaestus.md`'s migration table: `net.ios.config`
is `cisco.ios.ios_config`, `net.cli.command` is `ansible.netcommon.cli_command`. There is no
`cisco.ios.ios_facts` or `cisco.ios.ios_command` equivalent in the native catalog, so both
"record the version" steps use the generic `net.cli.command` with `show version`, the same way
you would reach for `ansible.netcommon.cli_command` if you did not want to pull in the whole
`cisco.ios` collection just to run one show command.

`pleiades.builtin.wait.port` and `pleiades.builtin.set_metadata` are different from every other
fqcn in this table: they are not ported from an Ansible module 1:1, they are genuinely native to
Pleiades, so they live under a reserved `pleiades.builtin.` namespace instead of a `net.*`/`pkg.*`
domain name. `set_metadata` is also reachable as the bare `set_metadata` (both spellings dispatch
identically, indefinitely); `wait.port`'s sibling methods, `wait.path` and `wait.search`, are not
yet renamed under this namespace, an intentionally left inconsistency rather than an oversight
(see `internal/forge/catalogdata/collections_gating.go`).

## What is identical

Pleiades is built as a strict superset of Ansible, and it shows here:

- `pre_tasks` / `tasks` / `post_tasks` on the Ansible side map straight onto `pretasks` /
  `tasks` / `posttasks` on the Pleiades side, same three-phase shape, same ordering.
- `block:` / `rescue:` work the same way in both: group the risky steps, catch failure with a
  handler, same nesting.
- `register:` works the same way in both: name a task's result so a later step can read it.
- Conditions on individual tasks are the same idea in both. Ansible's `when:` and Pleiades'
  `when:` both take one CEL/Jinja-free boolean expression or a list of them ANDed together
  (Pleiades' are CEL underneath, not Jinja, see below). Pleiades adds `when_or:` (list ORed
  instead of ANDed) and `when_cel:` (one raw CEL expression, for logic `when`/`when_or` cannot
  express) as its own extensions, but a plain `when:` reads identically in both files.
- `hosts:` at the top of the runbook is Ansible's own play-level `hosts:`: both files here set
  `hosts: sw1` once instead of repeating `target: sw1` on every task. It is a default, not a
  hard override: a task's own `params.target` (module-as-key sugar's bare `target:`) still wins
  when a task sets one, so a runbook can still mix a task with no target at all (a
  controller-side action, like `set_metadata` here) or one aimed at a different device with the
  rest sharing the `hosts:` default, in the same runbook.

## Two ways to write a Pleiades task

`upgrade_ios_xe.yaml` writes every task the explicit way:

```yaml
- name: Record the currently running version
  fqcn: net.cli.command
  params:
    command: "show version | include Version"
```

`upgrade_ios_xe_sugar.yaml` is the identical runbook written with module-as-key sugar instead:
the module name becomes the mapping key, and its arguments are that key's value directly, no
`fqcn:`/`params:` pair needed:

```yaml
- name: Record the currently running version
  net.cli.command:
    command: "show version | include Version"
```

Both compile to the exact same `*DAG` (confirmed: `pleiades validate` reports identical findings
against both files), and both syntaxes are supported forever, including mixed task by task within
one runbook. A task may set at most one non-reserved key: writing two module names on the same
task, or combining sugar with an explicit `fqcn:`/`block:`, is a clear build-time error rather
than a silent guess at which one you meant.

## What is different

**No Jinja templating in params, yet.** The Ansible playbook has a `vars:` block
(`target_image`, `target_md5`, `image_server`) and reuses `{{ target_image }}` five times.
Pleiades runbooks have no `vars:` section and no string interpolation in `params:` at all
(`internal/engine`'s `Task.Params` is a literal `map[string]interface{}`, nothing renders it).
The Pleiades runbook repeats the literal image filename and checksum in every task instead.
`docs/hephaestus.md` is explicit that a real template renderer is still a planned shared
primitive, not built yet, so this is a current gap, not a design choice you should copy.

**No block-level `when`.** Ansible lets you put `when:` on the whole `block:` and it gates
every task inside at once, which is how you would normally write "skip the whole upgrade if
we are already on the target version." The Pleiades runtime evaluates a task's condition once
per task, not once per block: a block task's own condition is never actually walked by the
executor (`internal/engine/dag.go`'s own comment: "the block task's own ID never appears as a
source or target in Adjacency"). Getting the same "skip everything" effect today means
repeating the same `when_cel:` on every task inside the block, which this example does not do,
to keep it readable. The Pleiades runbook here drops the "already on target version" fast-fail
instead of faking that gap away.

**`when_cel` for a check `when:` cannot express.** The gate before "Point the boot variable"
and "Reload" (`stat.md5_check["sw1"].stdout.matches("Verified")`) needs a substring check
against a specific device's registered command output. That is past what a bare `when:`
comparison does cleanly, so it uses `when_cel:`, the raw-CEL escape hatch. The equivalent
Ansible step uses `failed_when:` on the checksum task itself instead, a different mechanism
Pleiades does not have (there is no per-task `failed_when:` equivalent, see "Current status"
below): the runbook version reads the earlier task's result forward instead of failing that
task in place.

**`register_mask:` has no Ansible equivalent.** The first pretask, "Get Config", runs `show
running-config` and registers the result, then masks its own `stdout` field with `register_mask:
running_config.stdout` (the register name as an optional, `stat.<register>`-addressing-style
prefix; a bare `stdout` would work identically). Ansible has no per-value secrecy on a registered
result, only a whole-task `no_log: true`; Pleiades masks the named field the instant this same
task registers it, before anything downstream can see or publish it unmasked, the same guarantee
a password field gets. `pleiades run` (not shown running here, since nothing in this runbook
executes yet, see below) would still substring-scrub that value out of every later printed line
for the rest of the run, even one from a completely unrelated task that happens to echo it back.

**Credentials never live in the runbook or inventory file.** The Ansible side references
`{{ vault_catalyst_lab_password }}` and `{{ vault_catalyst_lab_enable_secret }}`, sourced from
a separate `ansible-vault` encrypted file not included here. Pleiades has no vars/vault
mechanism at all; instead you run `pleiades add-credential sw1 --username svc-netauto` once,
which prompts for the password and writes it to Pleiades' own encrypted secret store. Neither
`pleiades/inventory.yaml` nor `pleiades/runbooks/upgrade_ios_xe.yaml` ever mentions a password.

**Concepts with no Ansible equivalent.** None of these show up in the YAML because they are
not YAML: RBAC scoping of who can run this against `group:catalyst_lab`, the lock manager
serializing concurrent runs against the same switch, blast-radius computation before dispatch,
and the OTEL trace tying one "Run" click to every step across every device. `docs/
cisco_lab_scenario.md` walks through all of these for this exact scenario; they live in the
platform around the runbook, not in the file itself.

## Current status: this does not execute yet

Everything above describes the *shape* of the two files, not a claim that the Pleiades one
runs today. `net.cli.command`, `net.ios.config`, and `pleiades.builtin.wait.port` are registered
in the Forge collection catalog with the right capability and manifest metadata (so `pleiades
validate` can check them, and IDE tooling can discover them), but no dispatcher wires a declared
collection method to a real SSH transport yet. Running `pleiades validate` against this exact
runbook reports that honestly instead of pretending:

```
$ pleiades validate runbooks/upgrade_ios_xe.yaml
[collection] node "pretasks[0]": task pretasks[0] (name "Record the currently running version") calls "net.cli.command", which is declared but not yet implemented
...
pleiades: validation failed
```

`pleiades.builtin.set_metadata` does not show up in that output at all, and that is also honest,
not a gap: unlike `net.cli.command`/`net.ios.config`/`wait.port`, `set_metadata` is a real,
working, tested builtin today (`internal/engine/action.go`), so it is deliberately exempted from
the "declared but not yet implemented" check rather than routed through the same unbuilt
dispatcher every other collection name still needs.

and `pleiades run` refuses to execute for the same reason ("validation failed, not
executing"). `rescue:` has the same kind of gap one level down: the schema accepts it and
`pleiades validate` checks it structurally, but the Walk-tier executor does not run rescue or
always handlers yet either (`internal/engine/tasktree.go`: "there is no executor yet to give it
real meaning"). None of this is a mistake in this example. It is what
`pleiades forge new-collection`-generated stubs are supposed to do: report they are not
implemented rather than silently claim success.

If you want a runbook that actually executes against the current binary, `pleiades init`
scaffolds one (`runbooks/sample.yaml`, `fqcn: noop`). This example exists to show the DSL you
will write once the Phase 34 collection dispatcher lands, compared honestly against the Ansible
playbook it is meant to replace.

## Try it

```
cd examples/upgrade_ios/pleiades
pleiades validate runbooks/upgrade_ios_xe.yaml
pleiades validate runbooks/upgrade_ios_xe_sugar.yaml   # same findings, different syntax
```

```
cd examples/upgrade_ios/ansible
ansible-galaxy collection install cisco.ios ansible.netcommon   # once, if you don't have them
ansible-playbook -i inventory.ini upgrade_ios_xe.yml --syntax-check
```

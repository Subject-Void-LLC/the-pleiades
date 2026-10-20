# Upgrading a Cisco IOS-XE switch: Ansible vs. The Pleiades

This directory has the same upgrade written twice, so you can compare them line for line:

```
examples/upgrade_ios/
  ansible/
    inventory.ini
    group_vars/catalyst_lab.yml
    upgrade_ios_xe.yml       <- the Ansible playbook
  pleiades/
    inventory.yaml
    runbooks/upgrade_ios_xe.yaml         <- The Pleiades runbook
```

Both upgrade a Catalyst switch named `sw1` from whatever it is running today to IOS-XE
17.03.04: back up the config, copy the new image to flash, verify its checksum, point the
boot variable at it, reload, then confirm the new version came up.

For the general keyword map, module-to-FQCN map, and AWX/AAP object map this example is
built from, see [Migrating from Ansible, AWX, and AAP](../../docs/03-migrating-from-ansible.md).
This README covers only what is specific to these two files.

## Task-by-task

| Step | Ansible module | The Pleiades fqcn |
|------|-----------------|----------------|
| Get running config (masked) | `no_log: true` (a task attribute, not a module) | `net.cli.command` + `register_mask:` |
| Record current version | `cisco.ios.ios_facts` | `net.cli.command` |
| Check flash free space | `ansible.netcommon.cli_command` | `net.cli.command` |
| Back up running config | `cisco.ios.ios_config` (`backup: true`) | `net.ios.config` (`backup: true`) |
| Copy image to flash | `ansible.netcommon.cli_command` (answers a prompt) | `net.cli.command` (cannot; see below) |
| Verify image checksum | `ansible.netcommon.cli_command` | `net.cli.command` |
| Set boot variable, save | `cisco.ios.ios_config` (`save_when: modified`) | `net.ios.config`, then `net.ios.save` |
| Reload | `ansible.netcommon.cli_command` (answers a prompt) | `net.cli.command` (cannot; see below) |
| Wait for the switch to come back | `ansible.builtin.wait_for` | `pleiades.builtin.wait.port` |
| Re-check version | `cisco.ios.ios_facts` | `net.cli.command` |
| Record run report data | `ansible.builtin.set_stats` | `pleiades.builtin.set_metadata` |

There is no `cisco.ios.ios_facts` or `cisco.ios.ios_command` equivalent in the native catalog, so
both "record the version" steps use the generic `net.cli.command` with `show version`, the same
way you would reach for `ansible.netcommon.cli_command` if you did not want to pull in the whole
`cisco.ios` collection just to run one show command.

`pleiades.builtin.wait.port` and `pleiades.builtin.set_metadata` are different from every other
fqcn in this table: they are not ported from an Ansible module 1:1, they are genuinely native to
Pleiades, so they live under a reserved `pleiades.builtin.` namespace instead of a `net.*`/`pkg.*`
domain name. `set_metadata` is also reachable as the bare `set_metadata` (both spellings dispatch
identically, indefinitely); `wait.port`'s sibling methods, `wait.path` and `wait.search`, are not
yet renamed under this namespace, an intentionally left inconsistency rather than an oversight
(see `internal/forge/catalogdata/collections_gating.go`).

## How a Pleiades task is written

A task names its method as its key, and the method's parameters are that key's value:

```yaml
- name: Record the currently running version
  net.cli.command:
    command: "show version | include Version"
```

A task holds one method key. Two method names on one task, or a method beside `block:`, is a
build-time error rather than a guess at which you meant.

The engine also reads an older form, `fqcn:` with a `params:` map, so a runbook written that way
still runs; `pleiades validate` warns on each such task and names the method key to write
instead.

## What is different

**No authored `vars:`.** The Ansible playbook has a `vars:` block (`target_image`,
`target_md5`, `image_server`) and reuses `{{ target_image }}` five times. A Pleiades runbook
has no `vars:` section of its own: a string in `params:` renders, and can read the run's
variables (`pleiades run -e target_image=...`), earlier registered results, and in a
`rescue:` the failure it answers, but nothing in the file defines a variable. This runbook
repeats the literal image filename and checksum in every task, so it runs without `-e`; its
rescue does use templating, to quote the version the switch started from.

**No answers to interactive prompts.** The playbook's `cli_command` tasks answer IOS's own
questions with `prompt:` and `answer:` ("Destination filename" on copy, "[confirm]" on reload).
No native method can answer an interactive prompt yet, and `pleiades validate` refuses a
parameter a method does not declare, so the runbook does not pretend to pass them. The copy
works once the switch has `file prompt quiet` configured, which stops IOS asking. The reload
cannot be answered at all yet: that task waits on the prompt and fails after its 30 second
bound, so reload the switch by hand at that point. The runbook's comments say the same.

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
result, only a whole-task `no_log: true`; The Pleiades masks the named field the instant this same
task registers it, before anything downstream can see or publish it unmasked, the same guarantee
a password field gets. `pleiades run` also
substring-scrubs that value out of every later printed line
for the rest of the run, even one from a completely unrelated task that happens to echo it back.

**Credentials never live in the runbook or inventory file.** The Ansible side references
`{{ vault_catalyst_lab_password }}` and `{{ vault_catalyst_lab_enable_secret }}`, sourced from
a separate `ansible-vault` encrypted file not included here. The Pleiades has no vars/vault
mechanism at all; instead you run `pleiades add-credential sw1 --username svc-netauto` once,
which prompts for the password and writes it to The Pleiades' own encrypted secret store. Neither
`pleiades/inventory.yaml` nor `pleiades/runbooks/upgrade_ios_xe.yaml` ever mentions a password.

**Concepts with no Ansible equivalent.** None of these show up in the YAML because they are
not YAML: RBAC scoping of who can run this against `group:catalyst_lab`, the lock manager
serializing concurrent runs against the same switch, blast-radius computation before dispatch,
and the OTEL trace tying one "Run" click to every step across every device. They live in the
platform around the runbook, not in the file itself; see
[Start here](../../docs/01-start-here.md) for what is real and tested today.

## Current status

Every method this runbook calls is implemented, and `pleiades validate` passes both files:

```
$ pleiades validate runbooks/upgrade_ios_xe.yaml
validate: no issues found
```

Two things still stop it running end to end against a real switch, each stated at the task
it affects:

- The image copy needs `file prompt quiet` on the switch, since the step cannot answer IOS's
  "Destination filename" question (see "No answers to interactive prompts" above).
- The reload cannot answer "[confirm]" at all yet, so that step fails after 30 seconds and the
  switch has to be reloaded by hand.

When a task inside the block fails, its `rescue:` runs: it records the failure and the
rollback instructions in the run's metadata, and the run ends `rescued` rather than failed.

`pleiades.builtin.set_metadata` is a working builtin reached through the engine's own action path
rather than a Collection method, which is why it needs no catalog entry.

If you want a runbook that runs end to end against the current binary, `pleiades init`
scaffolds one (`runbooks/sample.yaml`, one `noop:` task), or see
[the Crawl-tier quickstart](../../docs/02-get-started.md), which runs a real `ssh_exec` task
against a real device.

## Try it

```
cd examples/upgrade_ios/pleiades
pleiades validate runbooks/upgrade_ios_xe.yaml
```

```
cd examples/upgrade_ios/ansible
ansible-galaxy collection install cisco.ios ansible.netcommon   # once, if you don't have them
ansible-playbook -i inventory.ini upgrade_ios_xe.yml --syntax-check
```

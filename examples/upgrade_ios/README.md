# Upgrading a Cisco IOS-XE switch: Ansible vs. The Pleiades

This directory has the same upgrade written three ways:

```
examples/upgrade_ios/
  ansible/
    inventory.ini
    group_vars/catalyst_lab.yml
    upgrade_ios_xe.yml                <- the Ansible playbook
  pleiades/
    inventory.yaml
    runbooks/upgrade_ios_xe.yaml      <- The Pleiades runbook, every answer written in it
    runbooks/upgrade_ios_xe_ai.yaml   <- the same, with a local model for what that one refuses
    ai/prompts/upgrade_triage.json    <- the prompt the AI-assisted runbook's rescue sends
```

All three upgrade a Catalyst switch named `sw1` from whatever it is running today to IOS-XE
17.03.04: copy the new image to flash, verify its checksum, point the boot statements at it,
save, reload, then confirm the new version came up.

For the general keyword map, module-to-FQCN map, and AWX/AAP object map this example is
built from, see [Migrating from Ansible, AWX, and AAP](../../docs/03-migrating-from-ansible.md).
This README covers only what is specific to these files.

## Task-by-task

| Step | Ansible module | The Pleiades fqcn |
|------|-----------------|----------------|
| Get running config (masked) | `no_log: true` (a task attribute, not a module) | `net.ios.command` + `register_mask:` |
| Record current version | `cisco.ios.ios_facts` | `net.ios.command` |
| Check flash free space | `ansible.netcommon.cli_command` | `net.ios.command` |
| Copy image to flash | `ansible.netcommon.cli_command` (`prompt:`/`answer:`) | `net.ios.command` (`prompts:`) |
| Verify image checksum | `ansible.netcommon.cli_command` + `failed_when:` | `net.ios.command` (fails on `%Error`) |
| Set boot statements, back up | `cisco.ios.ios_config` (`backup: true`) | `net.ios.config` (`backup: true`) |
| Save | `ios_config`'s `save_when: modified` | `net.ios.save` |
| Reload | `ansible.netcommon.cli_command` (`prompt:`/`answer:`) | `net.ios.command` (`prompts:`, `expect_disconnect:`) |
| Wait for the switch to come back | `ansible.builtin.wait_for` | `wait.connection` |
| Re-check version | `cisco.ios.ios_facts` | `net.ios.command` |
| Record run report data | `ansible.builtin.set_stats` | `pleiades.builtin.set_metadata` |

`net.ios.command` is the IOS sibling of the generic `net.cli.command`, as `net.ios.config` is of
`net.cli.config`. It turns IOS's pager off, so long output such as a running configuration comes
back whole; it needs no `cli_prompt` property on the device; and a line in IOS's error forms (`% ...`
or `%Error ...`) fails the task. A failed copy or a checksum mismatch therefore fails its task and
reaches the rescue, where the generic method would have reported the error text as a success.

`wait.connection` is Ansible's `wait_for_connection`. On a device that declares `CiscoIOSCapable`, as
`cisco_switch` does, each try logs in afresh and waits for the switch's prompt.

## How a Pleiades task is written

A task names its method as its key, and the method's parameters are that key's value:

```yaml
- name: Record the currently running version
  net.ios.command:
    command: "show version | include Version"
```

A task holds one method key. Two method names on one task, or a method beside `block:`, is a
build-time error rather than a guess at which you meant.

## Answering IOS's questions

IOS asks questions partway through some commands: copy asks for a destination filename, and asks
whether to overwrite a file already there; reload asks to confirm, and first whether to save when the
configuration changed. A task lists the ones it expects under `prompts:`, each with the answer to type
(`""` is Enter alone) or `refuse: true`:

```yaml
- name: Pull the new image onto flash
  net.ios.command:
    command: "copy http://10.10.10.5/images/cat9k_iosxe.17.03.04.SPA.bin flash:cat9k_iosxe.17.03.04.SPA.bin"
    prompts:
      - match: 'Destination filename \[[^\]]*\]\?'
        answer: ""
      - match: 'over ?write\? \[confirm\]'
        refuse: true
  timeout: 1800
```

- `match` is a regular expression found on the line the device is waiting on: the last line of output,
  which a question leaves open.
- Each entry answers once. A question asked again after its answer fails the task, since the answer did
  not take.
- `refuse` closes the session, which IOS takes as abandoning the command, and fails the task.
- A question nobody listed waits until the task's `timeout:`, and the failure names it.
- No text is ever sent to a question asking for a password, passphrase or secret: an answer in a task is
  never a secret.
- `expect_disconnect: true` on the reload counts the switch dropping the session after the confirmation
  as success.

The Ansible playbook's `answer: "\r"` becomes `answer: ""`, since Enter is always sent after an
answer.

## The AI-assisted runbook

`runbooks/upgrade_ios_xe_ai.yaml` is the same upgrade, with a local model (an Ollama on this
machine, `workstation` in the inventory) used in two places.

**For a question nobody listed.** The copy lists the filename question and leaves the overwrite
question to the model, under `ask:`:

```yaml
    ask:
      provider: workstation
      model: "gemma4:e4b-it-qat"
      residency: local
      choices:
        - id: overwrite
          answer: ""
          means: "Press Enter to replace a file named cat9k_iosxe.17.03.04.SPA.bin ..."
        - id: keep
          answer: "n"
          means: "Answer no and keep the file already on flash; the copy stops and this task fails."
      review:
        - field: confidence
          below: 0.7
```

- The model is sent the question as untrusted input, with each choice's `id` and `means`, and answers
  with one `id`, `refuse`, or `wait` (the line was not a question). Its answer is held to a schema whose
  only values are those.
- It never types text of its own. On IOS one Enter accepts a default filename and also confirms a
  reload, so a limit on what the model may type would not limit what typing it does. Only the author
  says what an answer does, and the choices are the author's.
- `refuse`, or an answer held for review with nobody at the terminal, closes the session and abandons
  the command. The reload's `ask` holds every answer for a person (`review: [always: true]`), so an
  unattended run never reloads on a model's say.
- A question listed under `prompts:` is never sent to the model.

**In the rescue.** When the block fails, the rescue lists flash and asks the model, through
`ai.chat` and `ai/prompts/upgrade_triage.json`, which of two fixed recoveries fits:
`remove_new_image` or `leave_for_operator`. The removal is a task of its own with a condition the
model cannot change: it runs only when the copy or the checksum is what failed (so the boot statements
never named the file), and when the switch is not already running 17.03.04. The model's pick and its
reason are recorded for the operator either way.

Run it after onboarding the model provider, which makes no model call:

```
cd examples/upgrade_ios/pleiades
ollama pull gemma4:e4b-it-qat
pleiades onboard workstation
pleiades validate runbooks/upgrade_ios_xe_ai.yaml
pleiades run runbooks/upgrade_ios_xe_ai.yaml
```

It runs on the Crawl tier only: a Walk Runner cannot reach `workstation` from a task on `sw1` yet.

## What else is different

**No authored `vars:`.** The Ansible playbook has a `vars:` block (`target_image`,
`target_md5`, `image_server`) and reuses `{{ target_image }}` five times. A Pleiades runbook
has no `vars:` section of its own: a string in `params:` renders, and can read the run's
variables (`pleiades run -e target_image=...`), earlier registered results, and in a
`rescue:` the failure it answers, but nothing in the file defines a variable. These runbooks
repeat the literal image filename and checksum in every task, so they run without `-e`; their
rescues do use templating, to quote the version the switch started from.

**`no boot system` first.** The boot statements are replaced, not added to: an older `boot
system` line left in place would be tried before the new image. `backup: true` captures the
running configuration, old boot statements included, as the task's `backup` stat (masked, since a
running configuration holds secrets). The Ansible playbook does the same.

**No block-level `when`.** Ansible lets you put `when:` on the whole `block:` and it gates
every task inside at once, which is how you would normally write "skip the whole upgrade if
we are already on the target version." The Pleiades runtime evaluates a task's condition once
per task, not once per block. Getting the same effect means repeating the same condition on
every task inside the block, which this example does not do, to keep it readable. The runbooks
drop the "already on target version" fast-fail instead of faking that gap away.

**No task-level retry yet.** Ansible's `retries`/`until`/`delay` could wait for `show version` to
report the new image in one step. The Pleiades has no such keyword yet, so the runbooks wait for
a login (`wait.connection`), then check the version once.

**`register_mask:` has no Ansible equivalent.** The first pretask, "Get Config", runs `show
running-config` and registers the result, then masks its own `stdout` field with `register_mask:
running_config.stdout`. Ansible has no per-value secrecy on a registered result, only a whole-task
`no_log: true`; The Pleiades masks the named field the instant the task registers it, before
anything downstream can see or publish it unmasked. `pleiades run` also scrubs that value out of
every later printed line for the rest of the run.

**Credentials never live in the runbook or inventory file.** The Ansible side references
`{{ vault_catalyst_lab_password }}` and `{{ vault_catalyst_lab_enable_secret }}`, sourced from
a separate `ansible-vault` encrypted file not included here. Instead you run `pleiades
add-credential sw1 --username svc-netauto` once, which prompts for the password and writes it to
The Pleiades' own encrypted secret store. Neither the inventory nor a runbook mentions a password.

**Concepts with no Ansible equivalent.** None of these show up in the YAML because they are
not YAML: RBAC scoping of who can run this against `group:catalyst_lab`, the lock manager
serializing concurrent runs against the same switch, blast-radius computation before dispatch,
and the OTEL trace tying one "Run" click to every step across every device. They live in the
platform around the runbook; see [Start here](../../docs/01-start-here.md) for what is real and
tested today.

## Current status

`pleiades validate` passes both runbooks (the AI-assisted one once `workstation` is onboarded).

What has been run, and against what:

- **Through the real binary, against a scripted switch.** `cmd/pleiades`'s
  `TestCLI_UpgradeIOSExamplesAnswerRefuseAndRecover` copies this directory and runs both runbooks
  over real SSH against an in-process server that asks IOS's questions, drops the session on reload,
  and answers the new version afterwards, with an HTTP server speaking Ollama's API as the model:
  - the listed answers upgrade the switch, and the running configuration's secret is in no output;
  - an image already on flash is refused, the run ends rescued, and nothing reloads;
  - the model answers the overwrite question it was given, and the upgrade completes;
  - the model keeps the file, the copy fails, and the rescue's model removes the image.

  The switch is a script and the model answers what the test tells it to. So the gate proves the
  runbooks answer, refuse, route a pick and recover as written, never that IOS or a real model
  behaves this way.
- **Not yet run against a real Catalyst switch.** No switch that may be reloaded is in the lab. The
  question texts follow IOS XE's; the live probe in `pkg/netcli` checks them, and IOS's error forms,
  against Cisco's DevNet sandbox with harmless commands (a copy to flash and its delete), never a
  reload.

When a task inside the block fails, its `rescue:` runs: it records the failure and what to do by
hand in the run's metadata, and the run ends `rescued` rather than failed. Nothing is undone
automatically: no step here records an undo, since IOS gives no reliable inverse for a boot change
or a reload.

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

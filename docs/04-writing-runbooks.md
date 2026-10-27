---
status: beta
---

# Writing runbooks: variables, loops, retries and failures

This page is for someone who writes native runbooks. It covers the keys that
give a runbook state and control flow: variables, `set_fact`, loops, retries
and the keywords that decide what counts as a failure. Every runbook below is
checked by `pleiades validate` each time the documentation is tested, so an
example here cannot drift from the parser. The full key list is generated on
the [task keys](reference/task-keys.md) page; this page is the guide to using
them together.

A task names its method as its key, with the method's parameters as the
mapping under it:

```text
- exec.command:
    cmd: uptime
```

Never write `fqcn:` with a separate `params:`. It still parses, and `validate`
warns on it.

## Variables and where they come from

A runbook reads a variable as `{{ vars.name }}` in a parameter and as
`vars.name` in a condition. Names use letters, digits and underscores. A
variable never carries a secret: a name or value shaped like one is refused.

The same name may be set in several places. **The narrower scope wins.**
From widest to narrowest, a name is taken from:

1. the runbook's `vars:`;
2. a global `set_fact` (it assigns in the runbook's own scope);
3. the device's own variables, from the inventory's `group_vars`, `host_vars`
   and its `properties.vars`;
4. a `set_fact` for the device the task runs on;
5. a `block`'s `vars:` (the outer block first);
6. the task's own `vars:`;
7. the run's extra variables (`pleiades run --extra-vars`, a launch's
   extra variables and survey answers).

The extra variables are not a scope. They are an override from outside the
runbook, so they win over everything above, as they do in Ansible.

A value may read other variables, such as `"{{ vars.base }}/app"`. A circle
of variables that read each other fails the task and names the circle. A
variable the run never gives is an error, not an empty string, unless the read
is guarded with `| default(...)`.

```yaml
# Writes the service's settings file from the runbook's variables.
# Blast radius: the web servers, one file each.
# Changes: /tmp/app-settings.txt.
# Undo: delete the file; the old text is kept out of the journal.
id: vars-basics
hosts: web
vars:
  base: /srv/app
  data_dir: "{{ vars.base }}/data"
tasks:
  - name: Settings file names the data directory
    file.copy:
      dest: /tmp/app-settings.txt
      content: "data_dir={{ vars.data_dir }}\n"
  - name: A task can bring a variable of its own
    vars:
      suffix: opt
    file.copy:
      dest: "/tmp/app-settings-{{ vars.suffix }}.txt"
      content: "base={{ vars.base }}\n"
```

Setting one name in two scopes is allowed, and the narrower one is read, but
`validate` warns and names which setting wins, since it is usually a surprise.
For example, with `base: /srv/app` in the runbook's `vars:` and `base: /opt/app`
in one task's `vars:`, that task reads `/opt/app` and every other task reads
`/srv/app`.

### Scope precedence differs from Ansible

Ansible ranks variables by a long fixed table where, for example, a play's
`vars:` beat inventory variables and a `set_fact` beats a task's `vars:`.
Pleiades uses the rule most languages use: the more local a definition, the
more it wins. A task's `vars:` beat its block's, which beat the device's, which
beat the runbook's. The one global exception is the extra variables, which
override from outside. When you convert a playbook that relied on Ansible's
table, read each variable that is set in two places.

### Variables from the inventory

Beside the inventory file a project may keep `group_vars/<group>.yml` and
`host_vars/<host>.yml`. A group is a tag: a host belongs to one group per tag
it carries, and `group_vars/all` applies to every host. Layered from widest to
narrowest, the last to name a variable wins:

1. `group_vars/all`;
2. `group_vars/<tag>` for each of the host's tags, in alphabetical order;
3. `host_vars/<host name>`;
4. the host's own `properties.vars` in the inventory.

A key is replaced whole, never merged into. A key or value shaped like a secret
is an error that names the file. These files are the inventory's
variables, so they sit at step 3 of the order above.

```text
# group_vars/web.yml
region: east
port: 8080

# host_vars/web2.yml
port: 9090
```

With those files, a task on `web2` reads `{{ vars.port }}` as `9090`, and one on
any other `web` host reads `8080`.

## set_fact

`set_fact` sets variables while the run goes. Each key is a fact name and each
value is rendered when the task runs. It has two options:

- By default a fact belongs to the **device the task ran on**. Only that
  device's later tasks see it. Two devices can hold different values under one
  name.
- `global: true` makes the fact visible to every device, as if the runbook had
  declared it. A later global `set_fact` replaces it.
- `no_override: true` keeps a value the name already has, so the first setting
  stands.

```yaml
# Computes a fact per device, shares one for the whole run, then uses both.
# Blast radius: the web servers, one file each.
# Changes: /tmp/fact-demo.txt.
# Undo: delete the file.
id: set-fact-scopes
hosts: web
vars:
  env: staging
tasks:
  - name: Each device builds its own label
    set_fact:
      label: "{{ device.name }}-{{ vars.env }}"
  - name: One value for every device
    set_fact:
      release: "2024.1"
      global: true
  - name: A default that never replaces an earlier value
    set_fact:
      channel: stable
      global: true
      no_override: true
  - name: Both facts are readable now
    file.copy:
      dest: /tmp/fact-demo.txt
      content: "{{ vars.label }} {{ vars.release }} {{ vars.channel }}\n"
```

`no_override` is for a default: when an earlier `set_fact` already set the name,
a later `no_override` one leaves that value alone. Setting one name twice in a
runbook makes `validate` warn which setting is read, so keep a name's settings
to what the run needs.

A fact is a value, not a template: a fact whose text holds `{{` stays text when
it is read later. A fact that holds a known secret is masked wherever it
appears, as a registered result is.

## Loops

`loop` runs a task once for each item of a list, in order, on each device.
`with_items` is the older name for the same thing; naming both is refused. The
list is written out, or is one `{{ }}` expression that renders to a list.

Inside the task, `{{ item }}` is the item and `{{ item.field }}` a field of
it. `loop.index` (from 1), `loop.index0`, `loop.first`, `loop.last` and
`loop.length` describe the position. A `when:` on the task is decided for each
item.

An item rendered into command text must be quoted with `| quote` (a shell) or
`| cli_token` (a network CLI), like any other value from data.

```yaml
# Makes three directories, one task for the whole list.
# Blast radius: the web servers.
# Changes: three directories under /tmp.
# Undo: remove the directories; a rollback does it, newest first.
id: loop-list
hosts: web
tasks:
  - name: One directory per name
    file.directory:
      path: "/tmp/{{ item }}"
    loop: [alpha, beta, gamma]
```

`loop_control` renames the item (`loop_var`), names the position (`index_var`)
and sets a `label`, which is kept with each result so a report says which item
it was without printing the item itself.

```yaml
# Looks at a list of paths and keeps each answer under its own label.
# Blast radius: read only on the web servers.
# Changes: nothing.
# Undo: nothing to undo.
id: loop-control
hosts: web
vars:
  paths: [/etc/hostname, /etc/hosts]
tasks:
  - name: Look at each path
    file.stat:
      path: "{{ path }}"
    loop: "{{ vars.paths }}"
    loop_control:
      loop_var: path
      index_var: n
      label: "path {{ n }}"
    register: seen
```

How a loop behaves:

- An item that fails does not stop the others. The task fails if any item did.
- Every item is its own result and its own journal entry (`<node>.item[k]`), so
  `pleiades rollback` undoes items one by one, newest first.
- A check runs every item as a check.
- `register` holds `results` (one entry per item with the item and the method's
  result) plus `changed`, `failed` and `skipped` for the whole.
- A loop runs at most 1000 items. More is refused when the runbook is built,
  or when a list read from data renders too long.
- A loop belongs on a method call, not a `block` or `parallel` group, and not
  with `rollback:`.

**Repeated blocks must be loops.** Writing the same task five times with one
value changed is the shape a loop exists for. A loop is shorter, validates
once, and journals each pass on its own.

## Retries with until

`until` runs a task again until its conditions hold. It reads the task's own
result under its `register`. `retries` is how many more runs it gets (default
3, at most 100) and `delay` the seconds between runs (default 5, at most 3600).

```yaml
# Waits for a marker file to appear after a deploy.
# Blast radius: read only on the web servers.
# Changes: nothing.
# Undo: nothing to undo.
id: until-wait
hosts: web
tasks:
  - name: Wait for the marker
    file.stat:
      path: /tmp/deploy.done
    register: marker
    until: ["result.marker.exists == true"]
    retries: 10
    delay: 6
```

- Each run is its own method call and its own journal entry
  (`<node>.attempt[k]`). Only the last run is registered.
- A run that fails is tried again; only the last run's failure fails the task.
  One that never holds fails the task, and the message says `until never held`.
- A check runs the task once and never retries.
- `until` needs a `register`, and `retries` or `delay` without `until` is refused.
- Retrying repeats a method's change, so `validate` warns on `until` over a
  method that is not read only.
- A loop and `until` may be combined: each item then gets its own retries.

## Deciding what a failure is

| Key | What it does |
| --- | --- |
| `failed_when` | Fails the task when its conditions hold, even if the method succeeded. |
| `changed_when` | Decides whether a finished run counts as having changed something. It changes what the report says, never what the journal recorded, so a rollback still undoes a change the method made. |
| `ignore_errors` | Lets the run go on past this task's failure. The failure is still recorded as one, marked ignored. No `rescue` runs for it. |
| `assert` | Fails when any condition in `that:` is false. `fail_msg` and `success_msg` are optional. |
| `fail` | Fails the task with the `msg` it is given. |
| `rescue` / `always` | A `block`'s answer to a failure, and the tasks that run after it either way. |

The conditions in `failed_when`, `changed_when`, `until` and `assert` are
written as a `when:` list is: each entry is an expression, and the list is
ANDed.

```yaml
# Reads a file's state, then checks it before any change is made.
# Blast radius: read only on the web servers.
# Changes: nothing.
# Undo: nothing to undo.
id: guards
hosts: web
tasks:
  - name: Look at the config
    file.stat:
      path: /etc/hostname
    register: cfg
    failed_when: ["result.cfg.size == 0"]
    changed_when: ["false"]
  - name: Refuse to go on without the file
    assert:
      that:
        - "result.cfg.exists == true"
      fail_msg: the config file is missing
  - name: A probe whose failure is fine
    file.stat:
      path: /tmp/optional-flag
    ignore_errors: true
```

`fail` is for a failure you decide on, usually under a `when:`:

```yaml
# Stops the run when the environment variable says it is not safe to go on.
# Blast radius: none, this runbook only decides.
# Changes: nothing.
# Undo: nothing to undo.
id: stop-here
hosts: web
vars:
  safe: true
tasks:
  - name: Stop when it is not safe
    fail:
      msg: not safe to go on
    when: ["vars.safe == false"]
```

A `block` pairs these with `rescue` and `always`. A rescue that finishes
without failing answers the failure and the run goes on; the rescue task reads
the failure it answers as `error.message`, `error.task` and so on.

```yaml
# Tries a step, and writes a note on the device when it fails.
# Blast radius: the web servers.
# Changes: /tmp/note.txt.
# Undo: delete the file.
id: rescue-note
hosts: web
tasks:
  - block:
      - name: The step that may fail
        fail:
          msg: the step failed
    rescue:
      - name: Leave a note about why
        file.copy:
          dest: /tmp/note.txt
          content: "{{ error.message }}\n"
    always:
      - name: Always runs
        noop: {}
```

## How long things live

| Thing | Set by | Lives for | Seen by |
| --- | --- | --- | --- |
| A variable | `vars:` on the runbook, a block or a task | The runbook, the block or the task | Tasks inside that scope, on every device |
| A device's variable | `group_vars`, `host_vars`, `properties.vars` | The run (read from the inventory when it starts) | Tasks on that device |
| A fact | `set_fact` | The rest of the run | Later tasks on that device. With `global: true`, later tasks on every device |
| A registered result | `register:` | The rest of the run | `result.<name>` when one device wrote it, and `nodes.<name>[<device id>]` for any device |
| An extra variable | `--extra-vars`, a launch, a survey | The run | Every task. It overrides every other source |
| A loop item | `loop:` | One pass of the task | That pass only |
| An `error` | A block's failure | The `rescue` that answers it | Tasks inside that `rescue` |

Nothing here outlives a run. A later run starts from the runbook, the
inventory and its own extra variables. Nothing carries a secret: the variables
that a bound credential injects are not readable as `vars`, and a registered
result is masked wherever it holds a known secret.

A device's task reads its own facts, and a global fact is read by all. A
registered result is the only way to pass a value from one device's task to
another's: read it through `nodes.<name>[<device id>]`.

## Templates in files

`file.template` writes a file whose text is a template in the project
directory. Name it in `src`; it renders when the task runs, with the same
variables as any other parameter, and may be written inside a loop.

```yaml
# Writes a templated file for each environment name.
# Blast radius: the web servers.
# Changes: three files under /tmp.
# Undo: delete the files.
id: template-files
hosts: web
vars:
  owner: platform
tasks:
  - name: One file per environment
    file.template:
      dest: "/tmp/app-{{ item }}.conf"
      content: "env={{ item }} owner={{ vars.owner }}\n"
    loop: [dev, test, prod]
```

## Reusing a task list like a function

A file of tasks that you `import_tasks` is a function, and the `vars:` on the
importing task are its arguments. The imported tasks read them as
`{{ vars.name }}`. Write the file once and call it as often as you like with
different values, so the same task is never copied.

```yaml fragment
# greet.yaml: a bare list of tasks
- name: say it
  exec.command:
    argv: ["echo", "{{ vars.greeting | default('hello') }} {{ vars.who }}"]
```

```yaml fragment
# the runbook calls it twice
tasks:
  - import_tasks:
      file: greet.yaml
    vars:
      who: world
  - import_tasks:
      file: greet.yaml
    vars:
      who: there
      greeting: hi
```

Write a default with the `default` filter, as above. A `vars:` that the
imported task sets for itself is the narrower scope, so it would win over the
caller's value and the caller could not change it. The imported file runs
with its caller's scope: it sees the runbook's `vars:`, the device's own
variables and any fact set so far.

## What is not supported

`with_*` loops other than `with_items`, `handlers` and `notify`, and `serial`
are not supported. `pleiades forge migrate-playbook` reports them for a person
to resolve. See [Migrating from Ansible](03-migrating-from-ansible.md).

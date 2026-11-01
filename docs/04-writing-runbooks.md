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
  result) plus `changed`, `failed` and `skipped` for the whole. The shape is
  below.
- `failed_when` and `changed_when` are decided for each item, against that
  item's own result: `result.<register>` in them is the item's result, not the
  list. An item `failed_when` fails is recorded with that item's journal entry
  as `failure_stage: failed_when`, apart from one the method failed (`action`).
- A loop runs at most 1000 items. More is refused when the runbook is built,
  or when a list read from data renders too long.
- A loop belongs on a method call, not a `block` or `parallel` group, and not
  with `rollback:`.

### What a looped task registers

A looped task registers a different shape from a task without a loop. A task
without a loop registers the method's result itself, so a later task reads
`result.seen.exists`. A looped task registers one object holding a list, so a
later task reads `result.seen.results[1].exists` for the second item, and
`result.seen.failed` for the whole. Changing a task from one call to a loop
changes what its `register` holds, and every later reader of it must change
too.

For the `loop-control` example above, `register: seen` holds this on each
device (the other fields `file.stat` returns, such as `kind` and `size`, are
left out here):

```json
{
  "results": [
    {"item": "/etc/hostname", "exists": true, "changed": false, "failed": false, "skipped": false},
    {"item": "/etc/hosts", "exists": true, "changed": false, "failed": false, "skipped": false}
  ],
  "changed": false,
  "failed": false,
  "skipped": false
}
```

- `results` has one entry per item, in the list's order. Each entry holds
  `item` (the item itself), every field the method returned for it, and
  `changed`, `failed` and `skipped` for that item. An item that failed also
  holds `msg`, the reason. An item retried with `until` holds its last try.
- `changed` is true when any item changed, `failed` when any item failed, and
  `skipped` when every item was skipped (a `when:` decided each one).
- A loop over an empty list registers `results: []`.

### `with_items`

`with_items` is accepted as another name for `loop`, with the same behavior
through the same code. Ansible itself now prefers `loop`, so new runbooks
should write `loop`. `with_items` is not deprecated in 0.3.x, and `validate`
does not warn about it. Naming both on one task is refused.

One difference from Ansible: its `with_items` flattens one level of nested
lists, and a runbook's does not, since it is `loop`. `pleiades forge
migrate-playbook` turns a playbook's `with_items` written as a list into
`loop` with the nesting already flattened. One written as an expression stays
`with_items`, and a nested list it renders to is not flattened.

**Repeated blocks must be loops.** Writing the same task five times with one
value changed is the shape a loop exists for. A loop is shorter, validates
once, and journals each pass on its own.

A loop item can name a project file only when the list is the author's: written in the runbook
or read from `vars`. A list read from a device's output cannot choose which file the run writes,
and `validate` refuses it. An item used in a network config line or a ping target needs
`| cli_token`, and one used in a shell command needs `| quote`.

A target chosen from data, such as `target: "{{ result.ticket.json.ci }}"`, needs `within:`
naming the device or tag it must stay inside, written out in the runbook. A loop's item cannot
choose the target, since the target is resolved once, before any item exists: to do one thing on
each device, put the devices in `hosts:`.

## Retries with until

In `until`, `changed_when` and `failed_when`, `result.<register>` is always the result of this run, for this device and
this loop item, even when other devices registered the same name. Another device's result is
`nodes.<register>[<device id>]`.

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
| `failed_when` | Fails the task when its conditions hold, even if the method succeeded. The journal records such a failure as `failure_stage: failed_when`, apart from a failure of the method itself (`action`). `ignore_errors` and `until` treat it as any failure. |
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

A template can loop and branch with Jinja's statements: `{% if %}` with
`{% elif %}` and `{% else %}`, `{% for x in list %}` with `{% else %}` for an
empty list, `{% set name = value %}`, `{# comments #}` and `{% raw %}`. Inside
a loop, `loop.index`, `loop.index0`, `loop.first`, `loop.last` and
`loop.length` count the items, and `{% for k, v in vars.map.items() %}` walks
a mapping's keys in sorted order.

`file.template` reads its template as Ansible's `template` module does:
`trim_blocks` is on, so a line that holds only a statement or a comment
leaves no blank line behind, and `lstrip_blocks` is off. Set either on the
task, `trim_blocks: false` or `lstrip_blocks: true`, to change it; with
`lstrip_blocks` a tag can be indented with the text around it. Both apply
to `content` and to a `src` file alike. Inside a tag, a `-` (`{%-`, `-%}`)
trims all the whitespace beside it and a `+` (`{%+`, `+%}`) keeps what the
two options would remove.

```yaml
# Writes a proxy's upstream list from the backends the runbook names.
# Blast radius: the web servers.
# Changes: one file under /tmp.
# Undo: delete the file.
id: template-statements
hosts: web
vars:
  backends: [app1, app2]
  tls: true
tasks:
  - name: Upstream list
    file.template:
      dest: /tmp/upstream.conf
      content: |
        upstream app {
        {% for name in vars.backends %}
          server {{ name }}:8080;
        {% endfor %}
        }
        {% if vars.tls %}
        listen 443 ssl;
        {% else %}
        listen 80;
        {% endif %}
```

That writes:

```text
upstream app {
  server app1:8080;
  server app2:8080;
}
listen 443 ssl;
```

A condition is a Jinja expression: `{% if vars.env == 'prod' and not
vars.drain %}`, `{% if 'web' in vars.roles %}`, `{% if vars.tls is defined %}`.
It has literals (`'text'`, `42`, `1.5`, `true`, `none`, `[a, b]`,
`{'k': v}`), the comparisons `==`, `!=`, `<`, `<=`, `>`, `>=`, `in` and
`not in`, `and`, `or` and `not`, the tests `is defined`, `is none`,
`is string`, `is number`, `is mapping`, `is iterable` and `is sameas`, `~` to
join text, and parentheses. Their meaning is Python's, as in Jinja: a number
never equals text, and ordering a number against text is an error, not false.
`and` and `or` stop at the first operand that decides, so the other is never
read. There is no arithmetic. A value is true unless it is false, none, zero
or an empty text, list or mapping. A name a template does not set and nobody
supplied is an error, except under `default` or `is defined`.
`pleiades doc filters` lists every statement, operator, test and filter.
A `set` inside a loop lasts for that pass only. One render runs
at most 10,000 loop passes in all, nested loops counted together, and writes
at most 1 MiB. `macro`, `include`, `import`, `extends`, `call` and every other
statement are refused by name: by `pleiades validate` for a template written
in the runbook, and before anything is written for a template file.

Every task parameter that holds `{{`, `{%` or `{#` renders, so a statement
alone is enough: `cmd: "{% if vars.drain %}systemctl stop app{% endif %}"`.
Every other parameter keeps Jinja's own whitespace defaults, with both
options off, as Ansible renders a module's arguments. Text meant for the
device itself, such as a shell's `printf '{%s}'` or a Jinja file copied onto
a host, is wrapped so it is sent as written:
`cmd: "printf '{% raw %}{%s}{% endraw %}' x"`. `pleiades validate` warns about
a parameter that holds `{%` or `{#` and no `{{`, since it may be such text.
Unlike Ansible's `template` module, a block tag keeps the newline after it,
which is Jinja's own default, so trim it with `-` as above.

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

## Running as another account (`become`)

A method that reaches its device over SSH runs its commands as the account that logs
in. When a change needs more than that account may do, say so with `become`, as in
Ansible:

```yaml
# Reloads the web tier's configuration as root.
# Changes: asks systemd on each web host to reload nginx.
# Undo: none needed; a reload of an unchanged configuration is harmless.
id: reload-web
hosts: web
become: true            # every task below runs as root, through sudo
tasks:
  - name: read the unit's state as the login, not as root
    exec.command:
      cmd: systemctl is-active nginx
    become: false       # the narrowest key wins
    ignore_errors: true
  - name: reload nginx as root
    exec.command:
      cmd: systemctl reload nginx
  - name: ask the web account what it is called
    exec.command:
      cmd: id -un
    become_user: www-data
```

`become` goes on a runbook, a block or a task, and a block's or a task's own key
replaces the one above it. `become_user` is the account (root when it is not given), and
`become_method`, if written, must be `sudo`.

What it is, and is not:

- It is `sudo -n`: the login needs a sudo rule that does not ask for a password
  (`NOPASSWD`). There is no sudo password to store, so none can reach a journal, a
  report or a command line. If sudo would ask for one, the task fails before any command
  runs, with sudo's own words and the account to give the rule to.
- Every command the method sends over its connection is elevated, including the reads
  and file writes the method makes to decide what to change, so a check sees what the
  real run will see.
- There is no `become_password`. The key is refused, so no sudo password is ever stored,
  sent or logged.
- It applies to a method that runs commands over SSH. `validate` refuses it on a built-in
  action, on a method that reaches its device another way (an API, a container engine), on
  a method that types into the device's CLI (`net.ios.*`, `net.cli.*`), on a method that
  reaches its service through a forward or subsystem the login opens (`db.pg.*`,
  `net.netconf.config`), and on `net.ssh.ping` and `wait.connection`, whose question is the
  login itself. A method that picks its carrier per device is refused when the task runs,
  on a carrier sudo cannot reach: a CLI session (`wait.routes` on Cisco IOS), WinRM (`svc.*`
  on Windows), a `gate.run` check's TCP precondition, or a container engine's socket reached
  through the login (`container.*`, after sudo has been checked). A method that reaches its
  device no SSH way on that device (a container engine over TLS) runs as the login and says
  so as a warning; nothing is refused there.
- The result and the journal record the account only when a connection really ran as it.
  A task that asked and was not run that way says so as a warning, and records no account.
  `pleiades rollback`, and a block's `rollback_on_failure` inside the run, run an undo as the
  account the change ran as. A task's `rollback:` steps run as its account too, so `validate`
  holds each step to the same rule: a step that could not run as it (`ssh_exec`, a method
  that does not use SSH) is refused before the run. A method that keeps something under the login's home (`net.frr.config`'s
  backups) records which account made it, and refuses an undo run as another.
- A Controller and Runner refuse a task with `become` for now. It runs from the CLI.

## Handlers and notify

A handler is a task that runs only when something before it changed, such as
restarting a service after its configuration file was written. List them under
`handlers:` and name them from a task with `notify:`:

```yaml
# Writes nginx's configuration and restarts nginx when it changed.
# Blast radius: the web servers, one file and one reload each.
# Changes: two files under /etc/nginx/conf.d, and restarts nginx.
# Undo: pleiades rollback restores the files; the restart is not undone.
id: web-config
hosts: web
become: true
tasks:
  - name: Write the site configuration
    file.copy:
      dest: /etc/nginx/conf.d/app.conf
      content: "server { listen 8080; }\n"
    notify: restart nginx
  - name: Write the upstream list
    file.copy:
      dest: /etc/nginx/conf.d/upstream.conf
      content: "upstream app { server 127.0.0.1:9000; }\n"
    notify: restart nginx
handlers:
  - name: restart nginx
    svc.systemd.restart:
      name: nginx
```

- A handler runs on a device only when a task that notifies it ran there and
  changed something. A task that failed, was skipped by `when:` or changed
  nothing notifies nothing.
- However many tasks notify it, a handler runs once per device, at the end of
  the section the tasks are in (`pretasks`, `tasks`, `posttasks`). Handlers run
  in the order `handlers:` lists them, not the order they were notified.
- `notify:` takes a handler's `name`, which must be unique, or a topic handlers
  `listen:` to, so one notification can run several handlers. A name nothing
  answers to is refused when the runbook is built.
- A failure that ends the run runs no handler, as in Ansible without
  `force_handlers`. A failure a `rescue:` answers does not end the run, so the
  rescue's own notifications are kept.
- `--tags` does not filter handlers: a handler a selected task notified runs.
- Not supported yet: `meta: flush_handlers`, a handler that notifies another
  handler, and a block as a handler. Each is refused with its reason.

## Rolling out a few devices at a time (`serial`)

`serial:` runs the whole runbook over a few of its hosts at a time, finishing every task on one batch
before the next batch starts, so a bad change stops after the first few devices instead of reaching all
of them:

```yaml
# Rolls the new release marker out to the web tier, two hosts at a time.
# Blast radius: every web host, two at a time; a failure stops the rollout.
# Changes: /etc/app/release on each web host.
# Undo: pleiades rollback restores the file.
id: rolling-release
hosts: web
serial: 2
tasks:
  - name: Mark the new release
    file.copy:
      dest: /etc/app/release
      content: "2.4.0\n"
```

- `serial:` takes a number of devices (`2`), a percentage (`"30%"`, rounded down, never less than one),
  or a list of them (`[1, "50%"]`), the last size repeating for every later batch, as in Ansible.
- Devices are batched in name order, so you can tell which devices go first.
- A batch with a failure stops the rollout there: later batches do not start, and the run says which
  devices it left alone. `max_fail_percentage` is not supported, since a failure already ends its batch's
  run on every device.
- The batches are one run: one journal, one report, and one `pleiades rollback`.
- Handlers run at the end of each batch's sections, as in Ansible.
- The CLI and a Controller both run batches, in the same order. On a Controller each device of a
  batch is its own dispatch, bounded by the launch's `forks` when it is set, and the next batch is
  dispatched once every device of the one before has reported. A failure there lets the rest of its
  batch finish, then skips every later device, naming the batch; on the CLI a failure ends the batch's
  run on every device. Either way no later batch starts.
- A Controller refuses a launch that asks for credential inputs at launch for a runbook with `serial:`:
  those inputs are held only while the job starts, and later batches start after that.

### Holding between steps (`pause`)

`pause:` holds the run for a time, most often at the end of a batch so a change can settle, or be
watched, before the next batch starts:

```yaml
# Rolls the release marker out two web hosts at a time, a minute apart.
# Blast radius: every web host, two at a time; a failure stops the rollout.
# Changes: /etc/app/release on each web host.
# Undo: pleiades rollback restores the file.
id: rolling-release-held
hosts: web
serial: 2
tasks:
  - name: Mark the new release
    file.copy:
      dest: /etc/app/release
      content: "2.4.0\n"
  - name: Let the batch settle before the next
    pause:
      minutes: 1
```

- `pause` takes `seconds` or `minutes`, a whole number, up to a day in all. It is also spelled
  `pleiades.builtin.pause`.
- It runs once for the run (once per batch under `serial:`), not once per host, and changes nothing.
- A check does not wait: it reports the pause and moves on.
- A pause that waits for a person (Ansible's `prompt`, or no duration) is refused, since a scheduled or
  dispatched run has no one to answer it.

## Long tasks (`async` and `poll`)

`async: N` gives a task a hard limit of N seconds on each device. Past it the task is stopped and fails,
which a `rescue:` can answer. `poll:` says how the run waits for it:

- `poll: M` (10 when `async` is set and `poll` is not): the task runs where it stands, and the run reports
  it still running every M seconds.
- `poll: 0`: the task runs in the background. Its `register` holds the job's id, `ansible_job_id`, and the
  run goes on to later tasks while it runs. An `async_status` task collects it.

```yaml
# Rebuilds the search index in the background while the caches are cleared.
# Blast radius: every search host, all at once.
# Changes: the search index; the page cache.
# Undo: none; a rebuild replaces the index, and a later rebuild restores it.
id: reindex-in-the-background
hosts: search
tasks:
  - name: Rebuild the index (up to an hour)
    exec.command:
      cmd: /usr/local/bin/reindex --full
    async: 3600
    poll: 0
    register: reindex
  - name: Clear the page cache meanwhile
    exec.command:
      cmd: /usr/local/bin/cache-clear
  - name: Wait for the rebuild
    async_status:
      jid: "{{ result.reindex.ansible_job_id }}"
```

- `async_status` (also spelled `pleiades.builtin.async_status`) waits for the job and returns the job's
  own result as its result: its change, its output, its failure. Ansible's version returns at once and is
  polled with `until: result.finished`. Here the wait happens inside it, so such a loop is satisfied the
  first time. A `timeout:` on the `async_status` task bounds the wait. A job that has not finished by then is
  not lost: the run still waits for it at the end.
- A device collects only its own job, and a job is collected once.
- `validate` refuses a background task that no later `async_status` collects. A run that ends anyway with a
  job nobody collected (a failure skipped the `async_status`) waits for it, up to its limit, before it
  ends, so its change is always in the journal.
- The job's change is journaled under its own task when it is collected, so `pleiades rollback` sees it.
  Its start changes nothing and is not journaled.
- A background job keeps its device's lock while it runs. Later tasks of the same run on that device go
  ahead, and another run is kept out until the job ends.
- On a device reached over SSH, an async task's commands run detached on the device: started in the
  background under `~/.pleiades-async-<uid>`, readable only by that account, and polled until they end.
  A command started this way keeps running when the connection drops. The polls reconnect, up to five times
  in a row, and read its output and exit status when it ends. At the limit, its whole process group gets
  `SIGTERM` and, up to ten seconds later, `SIGKILL`, and its directory is removed. A device with no POSIX `sh`
  runs the command where it stands, under the same limit. Commands that send input (a file upload) are
  never detached. A command's output is held in that directory until it is read back, which is the one
  place an async task's output touches the device's disk.
- A plain task's `timeout:` ends the task at its time on every device. It sends the command `SIGTERM`, but
  on some devices it never reaches the command (measured on Debian's sshd), so the command, and anything it started, may
  run on after the task has failed. When the work itself must stop, use `async:`.
- `async` goes on a task that calls a method. It is refused on a block, on a loop or a retry, and on
  `set_fact`, `assert`, `fail`, `pause` and `async_status`, which run in the engine at once. `poll:` needs
  `async:` and cannot be longer than it.

## What is not supported

`with_*` loops other than `with_items`, `meta: flush_handlers` and `max_fail_percentage` are not
supported. `pleiades forge migrate-playbook` reports them for a person
to resolve. See [Migrating from Ansible](03-migrating-from-ansible.md).

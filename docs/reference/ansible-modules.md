---
status: beta
---

# Ansible module conversions

What `pleiades forge migrate-playbook` does with each Ansible module it knows, generated from the
converter's own tables. A module not on this page is not converted: its task becomes an unrunnable
placeholder and the report raises `module.unmapped`. See the
[migration guide](../03-migrating-from-ansible.md) for the playbook keywords, conditions, loops and
variables around a module, and the [report schema](schemas/migration-report.json) for the report itself.

Each native call has a **class**, its state semantics: *asserted* names a desired state the method
compares before it acts, *computed* is a desired state decided only at run time, *imperative* has no
desired state (running it is the only way to learn what it does), and *observe* only reads.

## Modules that convert

### `ansible.builtin.package`

Also written as `package`.

| When | Native call | Class |
|---|---|---|
| `state`: `present`, `installed` | `pkg.install` | asserted |
| `state`: `absent`, `removed` | `pkg.remove` | asserted |
| `state`: `latest` | `pkg.upgrade` | asserted |

| Argument | Becomes |
|---|---|
| `name` | `name`, one task per item of a list |
| `state` | chooses the call |

### `ansible.builtin.apt`

Also written as `apt`.

| When | Native call | Class |
|---|---|---|
| `state`: `present` (the default) | `pkg.apt.install` | asserted |
| `state`: `absent` | `pkg.apt.remove` | asserted |
| `state`: `latest` | `pkg.apt.upgrade` | asserted |

| Argument | Becomes |
|---|---|
| `name`, `package`, `pkg` | `name`, one task per item of a list |
| `state` | chooses the call |
| `update_cache`, `update-cache` | dropped (`module.semantics`): the package index is not refreshed before the install, so an older version may be installed |
| `cache_valid_time` | dropped (`module.semantics`): the package index is not refreshed before the install |

### `ansible.builtin.dnf`

Also written as `dnf`, `ansible.builtin.yum`, `yum`.

| When | Native call | Class |
|---|---|---|
| `state`: `present`, `installed` (the default) | `pkg.dnf.install` | asserted |
| `state`: `absent`, `removed` | `pkg.dnf.remove` | asserted |
| `state`: `latest` | `pkg.dnf.upgrade` | asserted |

| Argument | Becomes |
|---|---|
| `name`, `pkg` | `name`, one task per item of a list |
| `state` | chooses the call |
| `update_cache`, `expire-cache` | dropped (`module.semantics`): the package metadata is not refreshed before the install, so an older version may be installed |

### `ansible.builtin.service`

Also written as `service`.

| When | Native call | Class |
|---|---|---|
| `state`: `started` | `svc.start` | asserted |
| `state`: `stopped` | `svc.stop` | asserted |
| `state`: `restarted` | `svc.restart` | imperative |
| `state`: `reloaded` | blocked (`args.value`): no native method reloads a service; restart it, or run the reload command | |
| `enabled`: `true` | `svc.enable` | asserted |
| `enabled`: `false` | `svc.disable` | asserted |

Each row that applies becomes its own native task, in the order above.

| Argument | Becomes |
|---|---|
| `name` | `name` |
| `state` | chooses the call |
| `enabled` | chooses the call |

### `ansible.builtin.systemd_service`

Also written as `systemd_service`, `ansible.builtin.systemd`, `systemd`.

| When | Native call | Class |
|---|---|---|
| `daemon_reload`: `true` | `svc.systemd.daemon_reload` | imperative |
| | ignores `name`, as Ansible does | |
| `daemon_reload`: `false` (the default) | no call | |
| `state`: `started` | `svc.systemd.start` | asserted |
| `state`: `stopped` | `svc.systemd.stop` | asserted |
| `state`: `restarted` | `svc.systemd.restart` | imperative |
| `state`: `reloaded` | blocked (`args.value`): no native method reloads a service; restart it, or run the reload command | |
| `enabled`: `true` | `svc.systemd.enable` | asserted |
| `enabled`: `false` | `svc.systemd.disable` | asserted |

Each row that applies becomes its own native task, in the order above.

| Argument | Becomes |
|---|---|
| `name`, `service`, `unit` | `name` |
| `state` | chooses the call |
| `enabled` | chooses the call |
| `daemon_reload`, `daemon-reload` | chooses the call |

### `ansible.windows.win_service`

Also written as `win_service`.

| When | Native call | Class |
|---|---|---|
| `state`: `started` | `svc.windows.start` | asserted |
| `state`: `stopped` | `svc.windows.stop` | asserted |
| `state`: `restarted` | `svc.windows.restart` | imperative |
| `state`: `reloaded` | blocked (`args.value`): no native method reloads a service; restart it, or run the reload command | |
| `start_mode`: `auto` | `svc.windows.enable` | asserted |
| `start_mode`: `disabled` | `svc.windows.disable` | asserted |
| `start_mode`: `manual`, `delayed` | blocked (`args.value`): no native method sets a Manual or delayed start type; svc.windows.enable sets Automatic and svc.windows.disable Disabled | |

Each row that applies becomes its own native task, in the order above.

| Argument | Becomes |
|---|---|
| `name` | `name` |
| `state` | chooses the call |
| `start_mode` | chooses the call |

### `ansible.builtin.file`

Also written as `file`.

| When | Native call | Class |
|---|---|---|
| `state`: `directory` | `file.directory` | asserted |
| | ignores `src`, as Ansible does | |
| `state`: `touch` | `file.touch` | imperative |
| | ignores `src`, as Ansible does | |
| `state`: `absent` | `file.remove` with `recurse: true` | asserted |
| | ignores `src`, `mode`, `owner`, `group`, as Ansible does | |
| `state`: `link` | `file.symlink` | asserted |
| `state`: `file` (the default) | `file.permissions` (review: file.permissions refuses a symbolic link, which Ansible follows, and needs at least one of mode, owner and group) | asserted |
| | ignores `src`, as Ansible does | |
| `state`: `hard` | blocked (`args.value`): no native method makes a hard link | |

| Argument | Becomes |
|---|---|
| `path`, `dest`, `name` | `path` |
| `state` | chooses the call |
| `src` | `src` |
| `mode` | `mode` |
| `owner` | `owner` |
| `group` | `group` |

### `ansible.builtin.copy`

Also written as `copy`.

| When | Native call | Class |
|---|---|---|
| otherwise | `file.copy` | asserted |

| Argument | Becomes |
|---|---|
| `dest` | `dest` |
| `content` | `content` |
| `src` | blocked (`args.unmapped`): Ansible looks src up in a role's files/ and the playbook's directory, and copies a directory whole; file.copy's src is one regular file named relative to the project directory, so rewrite the path, or put the content in the task |
| `validate` | blocked (`args.unmapped`): file.copy does not validate the new content before replacing the file |
| `backup` | dropped (`module.semantics`): no backup of the old file is kept |
| `mode` | `mode` |
| `owner` | `owner` |
| `group` | `group` |

The converter also writes the parameters whose native default differs from Ansible's, so the task does what the playbook did.

### `ansible.builtin.template`

Also written as `template`.

| When | Native call | Class |
|---|---|---|
| otherwise | `file.template` (review: the template file is rendered by the platform's renderer, which has Jinja's statements, operators and tests but reads a run variable as vars.<name> and a registered result as result.<name>, and Ansible's src is looked up in a role's templates/ while file.template's is relative to the project directory: rewrite the file's names and the path) | asserted |

| Argument | Becomes |
|---|---|
| `src` | `src` |
| `dest` | `dest` |
| `trim_blocks` | `trim_blocks` |
| `lstrip_blocks` | `lstrip_blocks` |
| `validate` | blocked (`args.unmapped`): file.template does not validate the new content before replacing the file |
| `backup` | dropped (`module.semantics`): no backup of the old file is kept |
| `force` | blocked (`args.unmapped`): file.template always makes the file match the template, which is force: true; a file written only when missing is a different task |
| `newline_sequence` | blocked (`args.unmapped`): the rendered file keeps the template's own newlines |
| `variable_start_string` | blocked (`args.unmapped`): the template's delimiters are Jinja's own and cannot be changed |
| `variable_end_string` | blocked (`args.unmapped`): the template's delimiters are Jinja's own and cannot be changed |
| `block_start_string` | blocked (`args.unmapped`): the template's delimiters are Jinja's own and cannot be changed |
| `block_end_string` | blocked (`args.unmapped`): the template's delimiters are Jinja's own and cannot be changed |
| `comment_start_string` | blocked (`args.unmapped`): the template's delimiters are Jinja's own and cannot be changed |
| `comment_end_string` | blocked (`args.unmapped`): the template's delimiters are Jinja's own and cannot be changed |
| `output_encoding` | blocked (`args.unmapped`): the rendered file is written as UTF-8 |
| `mode` | `mode` |
| `owner` | `owner` |
| `group` | `group` |

### `ansible.builtin.lineinfile`

Also written as `lineinfile`.

| When | Native call | Class |
|---|---|---|
| `state`: `present` (the default) | `file.line.set` | asserted |
| `state`: `absent` | `file.line.remove` | asserted |

| Argument | Becomes |
|---|---|
| `path`, `dest`, `destfile`, `name` | `path` |
| `state` | chooses the call |
| `line`, `value` | `line` |
| `regexp`, `regex` | `regexp` |
| `insertafter` | `insertafter` |
| `insertbefore` | `insertbefore` |

### `ansible.builtin.blockinfile`

Also written as `blockinfile`.

| When | Native call | Class |
|---|---|---|
| `state`: `present` (the default) | `file.block.set` | asserted |
| `state`: `absent` | `file.block.remove` | asserted |

| Argument | Becomes |
|---|---|
| `path`, `dest`, `destfile`, `name` | `path` |
| `state` | chooses the call |
| `block`, `content` | `block` |
| `marker` | `marker` |
| `marker_begin` | `marker_begin` |
| `marker_end` | `marker_end` |

### `ansible.builtin.command`

Also written as `command`.

| When | Native call | Class |
|---|---|---|
| otherwise | `exec.command` | imperative |

| Argument | Becomes |
|---|---|
| `cmd`, `_raw_params` | `cmd` |
| `argv` | `argv` |
| `chdir` | `chdir` |
| `creates` | `creates` |
| `removes` | `removes` |
| `stdin` | `stdin` |
| `stdin_add_newline` | blocked (`args.unmapped`): exec.command passes stdin exactly as given |
| `strip_empty_ends` | blocked (`args.unmapped`): exec.command reports stdout exactly as the command wrote it |
| `expand_argument_vars` | blocked (`args.unmapped`): exec.command never expands variables in its arguments |
| `warn` | dropped (`keyword.reporting`): it only controlled a warning |

A condition may read `cmd`, `rc`, `stderr`, `stdout` from its registered result.

### `ansible.builtin.shell`

Also written as `shell`.

| When | Native call | Class |
|---|---|---|
| otherwise | `exec.shell` | imperative |

| Argument | Becomes |
|---|---|
| `cmd`, `_raw_params` | `cmd` |
| `chdir` | `chdir` |
| `creates` | `creates` |
| `removes` | `removes` |
| `executable` | `executable` |
| `stdin` | `stdin` |
| `stdin_add_newline` | blocked (`args.unmapped`): exec.shell passes stdin exactly as given |
| `warn` | dropped (`keyword.reporting`): it only controlled a warning |

A condition may read `cmd`, `rc`, `stderr`, `stdout` from its registered result.

### `ansible.builtin.raw`

Also written as `raw`.

| When | Native call | Class |
|---|---|---|
| otherwise | `exec.shell` (review: raw runs the text through the account's login shell; exec.shell uses the shell the device declares, or /bin/sh, unless executable is given) | imperative |

| Argument | Becomes |
|---|---|
| `cmd`, `_raw_params` | `cmd` |
| `executable` | `executable` |

A condition may read `rc`, `stderr`, `stdout` from its registered result.

### `ansible.builtin.user`

Also written as `user`.

| When | Native call | Class |
|---|---|---|
| `state`: `present` (the default) | `identity.user.create` | asserted |
| | ignores `remove`, as Ansible does | |
| `state`: `absent` | `identity.user.remove` | asserted |
| | ignores `uid`, `group`, `shell`, `home`, `comment`, `create_home`, `system`, as Ansible does | |

| Argument | Becomes |
|---|---|
| `name`, `user` | `name` |
| `state` | chooses the call |
| `uid` | `uid` |
| `group` | `group` |
| `shell` | `shell` |
| `home` | `home` |
| `comment` | `comment` |
| `create_home`, `createhome` | `create_home` |
| `system` | `system` |
| `remove` | `remove` |
| `password` | blocked (`template.secret`): a password hash is a secret and is not copied into a runbook |

### `ansible.builtin.group`

Also written as `group`.

| When | Native call | Class |
|---|---|---|
| `state`: `present` (the default) | `identity.group.create` | asserted |
| `state`: `absent` | `identity.group.remove` | asserted |
| | ignores `gid`, `system`, as Ansible does | |

| Argument | Becomes |
|---|---|
| `name` | `name` |
| `state` | chooses the call |
| `gid` | `gid` |
| `system` | `system` |

### `ansible.builtin.unarchive`

Also written as `unarchive`.

| When | Native call | Class |
|---|---|---|
| `remote_src`: `true` | `archive.extract` | asserted |
| `remote_src`: `false` (the default) | blocked (`module.manual`): the archive is on the controller; copy it to the device first, then extract it with remote_src: true | |

| Argument | Becomes |
|---|---|
| `src` | `src` |
| `dest` | `dest` |
| `creates` | `creates` |
| `remote_src` | chooses the call |

### `ansible.posix.mount`

Also written as `mount`.

| When | Native call | Class |
|---|---|---|
| `state`: `mounted` | `fs.mount` with `persist: true` (review: Ansible creates a missing mount point directory; fs.mount fails instead) | asserted |
| `state`: `ephemeral` | `fs.mount` with `persist: false` (review: Ansible creates a missing mount point directory; fs.mount fails instead) | asserted |
| `state`: `unmounted` | `fs.unmount` with `persist: false` | asserted |
| | ignores `src`, `fstype`, `opts`, as Ansible does | |
| `state`: `absent` | `fs.unmount` with `persist: true` (review: Ansible also removes the mount point directory; fs.unmount leaves it) | asserted |
| | ignores `src`, `fstype`, `opts`, as Ansible does | |
| `state`: `present` | blocked (`args.value`): no native method writes an fstab entry without mounting it | |

| Argument | Becomes |
|---|---|
| `path`, `name` | `path` |
| `state` | chooses the call |
| `src` | `src` |
| `fstype` | `fstype` |
| `opts` | `opts` |
| `fstab` | `fstab` |

### `ansible.builtin.wait_for`

Also written as `wait_for`.

| When | Native call | Class |
|---|---|---|
| otherwise | `pleiades.builtin.wait.port` | observe |

| Argument | Becomes |
|---|---|
| `port` | `port` |
| `host` | `host` |
| `timeout` | `timeout` |
| `delay` | `delay` |
| `sleep` | `sleep` |
| `state` | `state` |
| `path` | blocked (`args.unmapped`): a wait on a path is wait.path's; write the task with it |
| `search_regex` | blocked (`args.unmapped`): a wait on content is wait.search's; write the task with it |

### `ansible.builtin.setup`

Also written as `setup`, `ansible.builtin.gather_facts`, `gather_facts`.

| When | Native call | Class |
|---|---|---|
| otherwise | `facts.gather` | observe |

| Argument | Becomes |
|---|---|
| `filter` | `filter` |
| `gather_subset` | dropped (`args.ignored`): facts.gather reads one small fixed set of facts, so there is no subset to choose |

### `ansible.posix.firewalld`

Also written as `firewalld`.

| When | Native call | Class |
|---|---|---|
| `state`: `enabled` | `fw.firewalld.allow` | asserted |
| `state`: `disabled` | `fw.firewalld.deny` | asserted |

| Argument | Becomes |
|---|---|
| `service` | `service` |
| `zone` | `zone` |
| `permanent` | `permanent` |
| `immediate` | `immediate` |
| `state` | chooses the call |
| `port` | blocked (`args.value`): Ansible writes port and protocol as one text (8080/tcp); write them as fw.firewalld's port and protocol |

The converter also writes the parameters whose native default differs from Ansible's, so the task does what the playbook did.

### `ansible.builtin.ping`

Also written as `ping`.

| When | Native call | Class |
|---|---|---|
| otherwise | `net.ssh.ping` | observe |

| Argument | Becomes |
|---|---|

### `ansible.netcommon.cli_command`

Also written as `cli_command`.

| When | Native call | Class |
|---|---|---|
| otherwise | `net.cli.command` | imperative |

| Argument | Becomes |
|---|---|
| `command` | `command` |
| `prompt` | blocked (`args.unmapped`): net.cli.command answers a question from prompts, a list of {match, answer}; write each prompt with its answer there |
| `answer` | blocked (`args.unmapped`): net.cli.command answers a question from prompts, a list of {match, answer}; write each answer with its prompt there |
| `sendonly` | blocked (`args.unmapped`): net.cli.command waits for the device's prompt; for a command that ends the session, such as reload, write expect_disconnect: true |
| `check_all` | blocked (`args.unmapped`): net.cli.command answers each prompts entry at most once, in whatever order the device asks; write the pairs as prompts |

A condition may read `stdout` from its registered result.

### `ansible.netcommon.cli_config`

Also written as `cli_config`.

| When | Native call | Class |
|---|---|---|
| otherwise | `net.cli.config` (review: cli_config sends only the lines missing from the running configuration; net.cli.config sends every line and always reports a change) | imperative |

| Argument | Becomes |
|---|---|
| `config` | `config` |

### `cisco.ios.ios_config`

Also written as `ios_config`.

| When | Native call | Class |
|---|---|---|
| otherwise | `net.ios.config` (review: ios_config sends only the lines missing from the running configuration; net.ios.config sends every line and reports a change only when the device's configuration differs afterwards) | asserted |

| Argument | Becomes |
|---|---|
| `lines`, `commands` | `lines` |
| `backup` | `backup` (review: the backup is kept as the task's backup stat, not written to a file beside the playbook) |
| `save_when` | dropped (`module.semantics`): the configuration is not saved; add a net.ios.save task after this one |
| `backup_options` | blocked (`args.unmapped`): net.ios.config records a backup as a stat, not a file |
| `parents` | blocked (`args.unmapped`): write the parent lines into lines in order |

### `cisco.ios.ios_facts`

Also written as `ios_facts`.

| When | Native call | Class |
|---|---|---|
| otherwise | `net.ios.facts` | observe |

| Argument | Becomes |
|---|---|
| `gather_subset` | `gather_subset` |

### `ansible.netcommon.netconf_config`

Also written as `netconf_config`.

| When | Native call | Class |
|---|---|---|
| otherwise | `net.netconf.config` with `lock: always` (review: Ansible edits the candidate datastore and commits when the device offers one; net.netconf.config edits running unless the task names datastore: candidate) | asserted |

| Argument | Becomes |
|---|---|
| `content`, `xml` | `content` |
| `target`, `datastore` | `datastore` |
| `default_operation` | `default_operation` |
| `error_option` | `error_option` |
| `lock` | `lock` |
| `commit` | `commit` |
| `backup` | `backup` (review: the backup is kept as the task's backup stat, not written to a file beside the playbook) |

The converter also writes the parameters whose native default differs from Ansible's, so the task does what the playbook did.

### `ansible.windows.win_optional_feature`

Also written as `win_optional_feature`.

| When | Native call | Class |
|---|---|---|
| `state`: `present` (the default) | `win.feature.install` (review: win.feature.install also enables the features it needs (DISM /all), which Ansible does only with include_parent: true) | asserted |
| `state`: `absent` | `win.feature.remove` | asserted |

| Argument | Becomes |
|---|---|
| `name` | `name`, one task per item of a list |
| `state` | chooses the call |
| `include_parent` | dropped (`args.ignored`): win.feature.install always enables the features a feature needs |
| `source` | blocked (`args.unmapped`): win.feature.install installs from the host's own store; a payload from another source is win.capability.install's source |

A condition may read `reboot_required` from its registered result.

### `ansible.windows.win_reboot`

Also written as `win_reboot`.

| When | Native call | Class |
|---|---|---|
| otherwise | `win.reboot` | imperative |

| Argument | Becomes |
|---|---|
| `reboot_timeout`, `reboot_timeout_sec` | `reboot_timeout` |
| `pre_reboot_delay`, `pre_reboot_delay_sec` | `pre_reboot_delay` |
| `post_reboot_delay`, `post_reboot_delay_sec` | `post_reboot_delay` |
| `connect_timeout`, `connect_timeout_sec` | `connect_timeout` |
| `msg` | `msg` |
| `test_command` | blocked (`args.unmapped`): win.reboot waits for the host to let its login in again, not for a command to pass; run the command in the next task |
| `boot_time_command` | blocked (`args.unmapped`): win.reboot reads the boot time itself |

### `ansible.windows.win_get_url`

Also written as `win_get_url`.

| When | Native call | Class |
|---|---|---|
| `checksum_algorithm`: `sha256` | `win.file.download` | asserted |
| `checksum_algorithm`: `md5`, `sha1`, `sha384`, `sha512` (the default) | blocked (`args.value`): win.file.download checks a SHA-256 only; give checksum_algorithm: sha256 and the file's SHA-256 as checksum | |

| Argument | Becomes |
|---|---|
| `url` | `url` |
| `dest` | `path` |
| `checksum` | `sha256` |
| `checksum_algorithm` | chooses the call |
| `force` | dropped (`args.ignored`): win.file.download fetches the file again only when the one there does not have the digest |

### `community.windows.win_firewall`

Also written as `win_firewall`.

| When | Native call | Class |
|---|---|---|
| `state`: `enabled` | `win.firewall.profile` with `enabled: true` | asserted |
| `state`: `disabled` | `win.firewall.profile` with `enabled: false` | asserted |
| otherwise | `win.firewall.profile` | asserted |

| Argument | Becomes |
|---|---|
| `profiles` | `name`, one task per item of a list |
| `state` | chooses the call |
| `inbound_action` | `default_inbound_action` |
| `outbound_action` | `default_outbound_action` |

### `ansible.windows.win_copy`

Also written as `win_copy`.

| When | Native call | Class |
|---|---|---|
| `remote_src`: `false` (the default) | `win.file.copy` (review: win.file.copy reads src from the project directory, not the playbook's files/ folder, and copies one file, not a directory: put the file there) | asserted |
| `remote_src`: `true` | blocked (`args.value`): no native method copies a file the host already has; remote_src: true has no conversion | |

| Argument | Becomes |
|---|---|
| `src` | `src` |
| `dest` | `path` |
| `remote_src` | chooses the call |
| `content` | blocked (`args.unmapped`): win.file.copy copies a file from the project; write the content to a file there and name it as src |
| `backup` | blocked (`args.unmapped`): win.file.copy keeps no backup; its undo puts the earlier file back |

### `community.windows.win_unzip`

Also written as `win_unzip`.

| When | Native call | Class |
|---|---|---|
| otherwise | `win.archive.extract` | asserted |

| Argument | Becomes |
|---|---|
| `src` | `src` |
| `dest` | `dest` |
| `creates` | `creates` |
| `delete_archive`, `rm` | `remove` |
| `password` | blocked (`template.secret`): an archive's password is a secret and is not copied into a runbook; win.archive.extract reads no password-protected archive |
| `recurse` | blocked (`args.unmapped`): win.archive.extract does not extract archives found inside the one it extracts |

## Modules a person converts

| Module | Why |
|---|---|
| `community.windows.win_firewall_rule`, `win_firewall_rule` | win_firewall_rule finds a rule by its display name, and win.firewall.rule by its Name, which for a rule Ansible made is a GUID; converted, the task would make a second rule beside it. Write win.firewall.rule with the rule's Name (Get-NetFirewallRule shows it) (`module.manual`) |
| `ansible.windows.win_feature`, `win_feature` | win_feature takes Server Manager names (Web-Server) and win.feature.install DISM's (IIS-WebServerRole), which differ; write the task with the DISM name (`module.manual`) |
| `community.windows.win_scheduled_task`, `win_scheduled_task` | win_scheduled_task takes lists of actions and triggers; win.schtask registers one of each, so a person chooses them (`module.manual`) |
| `ansible.builtin.assert`, `assert` | no native method stops a run on a condition; express it as when on the tasks that must not run (`module.manual`) |
| `ansible.builtin.fail`, `fail` | no native method fails a run on purpose (`module.manual`) |
| `ansible.builtin.pause`, `pause` | a timed pause is the native pause (seconds or minutes), written by hand until the converter maps it; a prompted pause waits for a person, which a run does not do (`module.manual`) |
| `ansible.builtin.script`, `script` | copying a local script to the device and running it is two native steps a person should write (`module.manual`) |
| `ansible.builtin.add_host`, `add_host` | the inventory is not changed by a run (`module.manual`) |
| `ansible.builtin.group_by`, `group_by` | the inventory is not changed by a run; tag the devices instead (`module.manual`) |
| `ansible.builtin.uri`, `uri` | uri calls the URL from the device, and http.request calls it from wherever the task runs; which side should call it is a person's decision (`module.manual`) |
| `ansible.builtin.get_url`, `get_url` | no native method downloads a file onto the device (`module.manual`) |
| `ansible.builtin.stat`, `stat` | no native method registers a path's attributes; a task that waits for a path is wait.path's, and one that runs only when a path is missing can use creates (`module.manual`) |

## Finding codes

Every finding in a migration report carries one of these codes. The set is closed: a code is added here before the converter can raise it.

| Code | Outcome | Meaning | Instead |
|---|---|---|---|
| `args.ignored` | info | An argument with no effect on the native call was dropped: Ansible ignores it for this state, or the native method has nothing for it to choose. | - |
| `args.list_unrolled` | review | A list of names became one task per name, so the items no longer succeed or fail together. | - |
| `args.unmapped` | blocked | An argument the native method has no equivalent for. | Drop it if it changes nothing, or write the task by hand. |
| `args.unparsable` | blocked | Free-form arguments this converter cannot read unambiguously. | Write the arguments as a map. |
| `args.value` | blocked | A value whose meaning differs between Ansible's YAML 1.1 and the runbook's YAML 1.2, or that the native parameter cannot take. | Quote the value in the playbook so its meaning is plain. |
| `assert.quiet` | info | assert's quiet option was ignored: the native assert always reports its result. | - |
| `assert.unmapped` | blocked | An assert, fail or set_fact that carries a keyword the native action cannot take (a loop, until, changed_when, failed_when, a register or a timeout), or an argument it does not have. | - |
| `block.always` | review | always: converted. It runs on each device the block ran on, whatever happened, or once on its own target. | - |
| `block.rescue` | review | rescue: converted. It runs on each device whose block failed, or once for a block one of whose tasks runs elsewhere (delegate_to becomes a target of its own, and such a block fails as one unit); Ansible's ansible_failed_task and ansible_failed_result are not translated, and the native rescue reads the failure as error. | - |
| `debug.dropped` | info | A debug task only prints, so it was dropped. | - |
| `handler.dropped` | review | A handler was not converted, since nothing native notifies it. | Run its task explicitly where it was notified. |
| `import.playbook` | blocked | import_playbook: convert the imported playbook on its own. | - |
| `import.tasks_missing` | blocked | import_tasks names a file that cannot be read inside the playbook's directory. | - |
| `include.role` | blocked | Roles convert through a Galaxy collection migration, not this one. | - |
| `include.tasks` | blocked | include_tasks decides at run time what it includes. | import_tasks, which is static. |
| `keyword.async` | blocked | async/poll has no native equivalent. | - |
| `keyword.become` | review | become dropped: the task runs as the connecting user and fails if it needs root. | Add become: true to the converted task (it runs sudo -n, so the login needs a NOPASSWD rule), or use a credential with the privilege. |
| `keyword.become_user` | blocked | become_user names a user other than root. | - |
| `keyword.changed_when` | blocked | changed_when could not be carried: it is templated, has no register to read, on a block, or its condition does not translate. | Write the condition as CEL over result.<register>. |
| `keyword.check_mode` | blocked | check_mode: true on a method that cannot be checked. | - |
| `keyword.check_mode_false` | review | check_mode: false dropped: the task now respects a check instead of running for real inside one. | - |
| `keyword.connection` | review | connection dropped: the transport comes from the device's capabilities. | - |
| `keyword.delegate_to` | blocked | delegate_to would run the task on another host, or on the controller with a native method that runs on the device. | Delegating to localhost converts when the native method runs in the host process. |
| `keyword.environment` | blocked | environment would change what the command sees. | - |
| `keyword.failed_when` | blocked | failed_when could not be carried: it has no register to read, is on a block, or its condition does not translate. | Write the condition as CEL over result.<register>. |
| `keyword.ignore_errors` | review | ignore_errors converted to the native key: the run goes on past a failure of the task, which is still recorded as a failure. Ansible's ignore_errors does not cover an unreachable host (ignore_unreachable does); the native key covers whatever fails the task. | - |
| `keyword.ignore_unreachable` | review | ignore_unreachable dropped: an unreachable device fails the run. | - |
| `keyword.local_action` | blocked | local_action or connection: local runs the task on the controller, and the native method runs on the device. | - |
| `keyword.local_satisfied` | info | delegate_to: localhost, local_action or connection: local dropped: the native method already runs in the host process. | - |
| `keyword.module_defaults` | blocked | module_defaults would change the call's arguments. | - |
| `keyword.no_log` | blocked | no_log hides output; dropping it could show a secret. | Use register_mask or secret_mask on the fields that hold the secret. |
| `keyword.notify` | review | notify dropped: the handler will not run. | Run the handler's task explicitly after the change, guarded by when. |
| `keyword.reporting` | info | A reporting-only keyword (diff, debugger) was dropped. | - |
| `keyword.retries` | blocked | until, retries or delay could not be carried: no register to read, a templated or out-of-range count (retries 0 to 100, delay 0 to 3600), a condition that does not translate, or the keyword is on a block. | Write the condition as CEL over result.<register>. |
| `keyword.run_once` | blocked | run_once: the native task would run on every device. | - |
| `keyword.tags` | blocked | tags that are templated, or that name all, tagged or untagged, which a runbook task cannot carry. | Write the tag names. |
| `keyword.throttle` | blocked | throttle has no native equivalent. | - |
| `keyword.timeout` | blocked | A timeout the native task cannot carry: templated or not a whole number of seconds up to a day, or on a play, a block or an import_tasks task, since a native timeout bounds one task. | Write a literal number of seconds on each task. A literal timeout on a task converts to the runbook's own timeout key. |
| `keyword.unknown` | blocked | A key this converter does not know. | - |
| `loop.control` | blocked | A loop_control option this converter does not reproduce. | - |
| `loop.native` | review | A loop became the native loop: the task runs once per item at run time, on each device, and a failing item does not stop the others. Its register holds results, changed, failed and skipped, as Ansible's does. | - |
| `loop.register` | blocked | A task that becomes several native tasks cannot hold one registered name or one loop. | - |
| `loop.runtime` | blocked | A loop over a list the native loop cannot read: a lookup, a dictionary, or an expression reading something other than vars, a registered result or the device. | Write one task per item. |
| `loop.too_long` | blocked | A loop longer than this converter unrolls. | - |
| `loop.unrolled` | info | A loop over a list known here became one task per item. | - |
| `meta.dropped` | info | A meta task with nothing to run (flush_handlers, noop) was dropped. | - |
| `meta.unsupported` | blocked | A meta task that changes how the run proceeds. | - |
| `module.ambiguous` | blocked | The task names more than one module, or none. | One module per task. |
| `module.manual` | blocked | This module needs a person: it has no faithful mechanical conversion. | - |
| `module.semantics` | review | The native method does what the module does, except in the way the finding says. | - |
| `module.unmapped` | blocked | No native method does what this module does. | Write the task by hand with a native method, or run the playbook unchanged as a playbook job. |
| `play.facts` | info | gather_facts dropped: facts are not gathered. | facts.gather, where the device supports it. |
| `play.hosts_all` | review | hosts: all needs an inventory tag named all. | - |
| `play.hosts_local` | review | hosts: localhost has no native equivalent; the tasks run with no target. | - |
| `play.hosts_pattern` | blocked | hosts: is a pattern or a list, which a runbook's single host or tag cannot express. | Tag the devices and name the tag. |
| `play.identity` | review | remote_user, port or connection dropped: identity and transport come from the inventory and credential store. | - |
| `play.malformed` | blocked | A play that is not a map, or has no tasks. | - |
| `play.order` | review | A play-level control (strategy, order, max_fail_percentage, force_handlers) was dropped: the run stops at the first failure. | - |
| `play.roles` | blocked | roles: convert through a Galaxy collection migration. | - |
| `play.serial` | blocked | serial: the runbook would change every device at once. | - |
| `play.split` | review | This play's hosts differ from the play before it, so it starts a new runbook; run them in order. | - |
| `play.vars_prompt` | blocked | vars_prompt asks at run time. | A survey on the job template. |
| `set_fact.cacheable` | review | set_fact converted to the set_fact action; its cacheable option was dropped, since a runbook's facts last for the run only. | - |
| `set_fact.resolved` | info | A set_fact with only literal values, each variable defined once, was folded into the variables it defines. | - |
| `set_fact.runtime` | blocked | A set_fact that is looped, or an include_vars, sets its value only at run time and has no native form. | - |
| `state.computed` | blocked | The task's state comes from a variable with no single value, so which native method it maps to is decided only at run time. | Choose the method that matches the state you mean. |
| `template.fact` | blocked | A template reads a fact or a magic variable (inventory_hostname, ansible_*), known only on a host at run time. | Put per-device values in inventory properties. |
| `template.quoted` | review | A {{ }} expression in command text was given the quote filter (cli_token for a network CLI), which the native command-text rule requires: the value reaches the command as one safe word, where Ansible pasted it as written. | - |
| `template.register` | review | A template reads a registered result as result.<name>, which exists only when exactly one device (or one device-less task) wrote it; with several devices, read nodes.<name>[<deviceID>]. | - |
| `template.resolved` | info | A template read a variable with exactly one literal value in the playbook, and was replaced by that value. | - |
| `template.secret` | blocked | The variable's name or value looks secret, so it is not copied into the runbook. | Bind a credential to the template instead of writing the value. |
| `template.unresolved` | blocked | A template reads a variable with no single literal value here: set at run time, defined more than once, a fact, or undefined. | Write the value, or wait for templated params. |
| `template.unsupported` | blocked | A template uses Jinja this converter cannot evaluate (a filter, a test or a statement). | Write the resulting value. |
| `vars.files` | review | A vars_files entry could not be read inside the playbook's directory, so its variables are unknown here. | - |
| `vars.inventory` | review | group_vars or host_vars exist beside the playbook. This conversion does not read them; the static inventory does, as each device's own variables (read as vars.<name>), and a device's variable beats the runbook's vars where Ansible lets a play's vars beat it. | Keep group_vars and host_vars beside the inventory, and check any name the playbook also sets in vars. |
| `vars.precedence` | review | The same variable is set at more than one scope. Ansible lets a play's vars beat inventory vars and a set_fact beat a task's vars; the runbook's rule is that the narrower scope always wins (runbook, global fact, device inventory vars, device fact, block, task, extra vars), so the winner can differ. | Give the variable one name per meaning. |
| `vars.unreadable` | blocked | A vars entry cannot be carried: its name is not a variable name the runbook reads, or it holds a secret or a vault value. | Bind a credential to the template for a secret. |
| `vault.value` | blocked | A vault-encrypted value. It is never read or copied. | Bind a credential to the template instead. |
| `when.fact` | blocked | A condition reads a fact or a magic variable (inventory_hostname, ansible_*). | - |
| `when.register` | review | A condition reads a registered result. A native condition is evaluated once for the task, across every device it targets, not once per host. | - |
| `when.unsupported` | blocked | A condition (when, until, changed_when, failed_when or an assert's that) uses Jinja this converter cannot translate to CEL. | Write the condition as when_cel. |
| `yaml.duplicate_key` | blocked | A map repeats a key; Ansible keeps the last and warns. | - |
| `yaml.limit` | blocked | The playbook exceeds a size, depth or alias limit. | - |

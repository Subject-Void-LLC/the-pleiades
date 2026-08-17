---
status: beta
---

# wait.search

Waits for a pattern to appear in a file's contents on the target.

Reads a file on the device on an interval until search_regex matches it, or until it stops matching when state is absent, and fails when the timeout runs out first. It changes nothing. The whole file is read on every look, so this suits a service's startup log rather than a file that grows without limit. The pattern is matched in multiline mode, as wait_for matches it, so ^ and $ mean the start and end of a line; it is a Go RE2 pattern, so a backreference or a lookahead is refused when the task is read rather than silently never matching. Only a regular file is read: a path that is missing, or that is a directory, counts as not matching, which satisfies state: absent at once and never satisfies state: present.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The file on the device to read. |
| `search_regex` | `string` | yes | - | The pattern to look for, matched in multiline mode against the whole file. Use wait.path when only the file's existence matters. |
| `state` | `string` | no | `present` | Whether to wait for the pattern to match (present) or to stop matching (absent). started and stopped are accepted as wait_for's own spellings of the same two. drained is refused, since it asks about a socket's send queue and means nothing for a file. |
| `timeout` | `int` | no | `300` | How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0. |
| `delay` | `int` | no | `0` | How many seconds to wait before reading the first time. Useful when the file still holds the previous run's matching line. Must be shorter than the timeout, which it is spent out of. |
| `sleep` | `int` | no | `1` | How many seconds to wait between reads. Must be more than 0: a sleep of zero would read the file as fast as the device can send it. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file this task read. |
| `search_regex` | `string` | always | The pattern this task looked for, exactly as the task wrote it. |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `match_groups` | `list of string` | when the pattern matched | The capture groups of the match, in order, without the whole match itself. |
| `match_groupdict` | `dict` | when the pattern matched | The named capture groups of the match, keyed by name. Empty when the pattern names none. |
| `diff` | `dict` | always | What the last read found: the path, whether it was a readable file, the pattern, and whether it matched. Both halves are identical, since a wait changes nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method reads a file and changes nothing on the device, so there is nothing to undo. That is the same fact that makes it report changed: false. It still records a diff whose two halves are identical, so a journal can tell an observation from a task that was never recorded.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.path`
- `pleiades.builtin.wait.port`

## Examples

Wait for a service to log that it started:

```yaml
- name: Wait for the API to finish starting
  fqcn: wait.search
  params:
    path: /var/log/pleiades/api.log
    search_regex: "^Listening on "
    timeout: 120
```

Capture the port a service chose:

```yaml
- name: Read the port out of the log
  fqcn: wait.search
  params:
    path: /var/log/app.log
    search_regex: "listening on port (?P<port>[0-9]+)"
  register: startup
```

Wait for an error line to be rotated away:

```yaml
- name: Wait for the log to stop showing the failure
  fqcn: wait.search
  params:
    path: /var/log/app.log
    search_regex: "FATAL"
    state: absent
    timeout: 60
    sleep: 5
```


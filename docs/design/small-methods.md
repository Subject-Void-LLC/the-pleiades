# Design note: `file.json`, `file.yaml`, `win.schtask` and `http.check`

Status: design. Four methods in namespaces that already exist, each closing a gap a runbook otherwise fills with
a shell one-liner.

## Methods

| Method | Does | Site, device, transport |
|---|---|---|
| `file.json` | Sets, or removes, the value at a key path in a JSON file on the device, preserving every other key, their order, the numbers as written, and the file's indentation. | target, required, ssh |
| `file.yaml` | The same for a YAML file, preserving comments, order and the rest of the document. | target, required, ssh |
| `win.schtask` | Registers, updates or removes a Windows scheduled task, or inspects one, over WinRM. | target, required, winrm |
| `http.check` | Requests a URL and checks its status and, optionally, its body, following a bounded number of redirects, never returning or logging the body. | controller, none, none |

## `file.json` and `file.yaml`

- **Parameters.**
  - `path`;
  - `key`: a dotted path (`server.port`), or a list of segments (`[server, "a.b", 0]`) for a key holding a dot.
    An integer segment indexes a list;
  - `value`: any YAML value;
  - `state`: `present` (the default) or `absent`;
  - `create`: start from an empty document when the file does not exist; the default is to refuse.
- **What is preserved.** JSON is read by an ordered parser kept beside its encoder (`pkg/jsonedit`). It refuses a
  duplicate key, which two readers resolve differently. It keeps every number exactly as written, so a 64-bit id
  survives, and keeps the key order. It writes back with the indentation the file used: tabs, or a number of
  spaces, or none for a one-line file.

  YAML is edited as `go.yaml.in/yaml/v3` nodes, so comments and order survive. The rest of the document is
  re-emitted in that library's style (indentation and quoting may be normalized), which the method says in its
  documentation rather than promising byte-for-byte preservation. A path through an alias is refused, since
  setting it would change every place that alias is used. So is a stream of several documents.
- **The write** goes through `pkg/remotefile.Replace`, which keeps the file's owner, group and mode, and is atomic
  where the directory allows it. No change is no write.
- **Check** computes the new document exactly as a run does and reports whether it differs, with a diff of the
  key's old and new value, masked as every diff is. It writes nothing.

## `win.schtask`

- **Parameters.**
  - `name`: a task path such as `\Pleiades\Backup`;
  - `state`: `present`, `absent` or `query`;
  - for `present`: `execute` and `arguments`, `working_directory`, one trigger (`at_startup`, `at_logon`,
    `daily: "02:30"`, or `once: <RFC 3339 time>`), `run_as` (`SYSTEM`, or the device's own login), `enabled`, and
    `description`.
- **How.** One PowerShell script per call, through `pkg/winrmexec`, using the ScheduledTasks cmdlets. Every value
  reaches the script quoted by `winrmexec.QuotePS`, and every name is first held to a strict pattern. The task's
  current definition is read first (`Export-ScheduledTask`), and a task that already matches is no change.
- **Undo.**
  - A task this run registered: its removal.
  - A task this run changed or removed: re-registering its previous definition. The previous definition is the
    task's XML from the device, so it is withheld from the journal, as a file's earlier content is, and a rollback
    replays it only from the task's `rollback:` list.
- **Check** reads and compares, and reports what would change. `query` only reads.

## `http.check`

- **Parameters.**
  - `url` (http or https);
  - `status`: one code or a list, default 200;
  - `body_contains` or `body_matches` (RE2);
  - `method`: `GET` or `HEAD`;
  - `max_redirects`: 0 to 10, default 5;
  - `timeout`;
  - `validate_certs`: default true, and false is loud.
- **No body anywhere.** The body is read, at most 4 MiB, only to match it. What is returned is the status, the
  final URL, the redirects followed, the elapsed time, the body's size and SHA-256, and whether it matched. The body
  itself is never returned, logged or put in an error.
- **Redirects.** At most `max_redirects` are followed. A redirect from https to http is refused, since the check
  would then vouch for a page an attacker on the path could have served.
- **Check equals Invoke:** `GET` and `HEAD` are safe methods.

## Check and Reversibility

| Method | Check | Reversible |
|---|---|---|
| `file.json`, `file.yaml` | Computes the edit and reports the diff; writes nothing. | Yes. The undo is `file.copy` of the earlier content, withheld from the journal; a file the run created is removed. |
| `win.schtask` | Reads the task and reports what would change. | Yes. A registration's undo removes the task; a change's or a removal's re-registers the earlier definition, withheld. |
| `http.check` | Same as Invoke. | Read-only. |

## Failure modes

| Failure | Answer |
|---|---|
| The file is not JSON or YAML, holds a duplicate key, or is over 4 MiB | Refused, naming where, and nothing is written. |
| The key path crosses a value that is not a mapping or a list, or an index past a list's end | Refused, naming the segment. |
| A YAML path through an alias, or a multi-document stream | Refused. |
| A task name or value outside its pattern | Refused before WinRM is reached. |
| The scheduled task cmdlets fail | Fails with PowerShell's own error, bounded. |
| An unexpected status, or a body that does not match | Fails, naming the status and the body's size and digest, never its text. |
| More redirects than allowed, or a downgrade to http | Fails, naming the hop. |

## What is proved, and how

- `file.json` and `file.yaml` run against a real SSH server, with the edit fuzzed for round trips.
- `http.check` runs against real HTTP servers in the test process.
- `win.schtask`'s script and its parsing are tested against recorded PowerShell output. It runs against a real
  Windows host only where one is configured (the experimental `winrm` CI job and the lab), and the final report
  says which.

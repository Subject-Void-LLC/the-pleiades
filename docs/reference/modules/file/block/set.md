---
status: beta
---

# file.block.set

Ensures a marked, multi-line block of text is present in a file.

Keeps a region of a text file, delimited by a begin and an end marker line, exactly as the runbook declares it. The markers are what make this safe to run twice: the region carries its own boundaries on the device, so a later run replaces the text between them in place rather than appending a second copy below the first. A file with no such markers gets the block, with its markers, appended at the end. A file whose block already matches is not written at all. The file must already exist, since file.touch and file.copy are what create; a symbolic link is refused, because writing the file back would replace the link with a regular file. Its mode, owner and group are put back after every rewrite, so managing a block in a file that services read does not quietly make it private. Markers that do not pair up (two begin markers, or a begin with no end) are an error rather than a guess about where the block stops.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The file to edit. It must already exist: this method edits a file rather than creating one. |
| `block` | `string` | yes | - | The text to keep between the markers, usually written as a YAML block scalar. A trailing newline is not significant, so the same block written inline and as a block scalar produce the same region. It must not be empty and must not itself contain a marker line. |
| `marker` | `string` | no | `# {mark} ANSIBLE MANAGED BLOCK` | The template for both marker lines. The {mark} placeholder is replaced by marker_begin on the line above the block and by marker_end on the line below it. Change it for a file whose comment character is not #, and keep it stable afterwards: a task that changes its marker stops finding the block it wrote last time and appends a second one. |
| `marker_begin` | `string` | no | `BEGIN` | The word {mark} becomes on the line above the block. |
| `marker_end` | `string` | no | `END` | The word {mark} becomes on the line below the block. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file this task acted on. |
| `present` | `bool` | always | Whether a marked block is in the file now, read back from the device. |
| `block` | `string` | always | The text between the markers now, with no trailing newline. |
| `diff` | `dict` | always | The before and after state of the marked region, each holding whether the block was present and what was between the markers. Recorded even when nothing changed, in which case the two halves are identical. |
| `inverse` | `dict` | when the run changed the file | The task that undoes this run: a file.block.set carrying the previous body, or a file.block.remove when the file carried no block before. A converged run records none, which is how the journal says undoing it means doing nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

The strongest undo in the file namespace, and the markers are why: they delimit on the device the exact region this run replaced, so a run that changed a block emits an instruction restoring the previous body verbatim, and a run that added one emits a file.block.remove. file.line is weaker because it identifies its target by a pattern rather than by a boundary, so a later edit can make that pattern match a different line and its undo cannot promise the text lands where it came from. What this still does not restore: anything outside the markers, the file's mode and owner (which this method never changes, and puts back after each rewrite), and a final newline added to a file that had none, which the emitted inverse says in its own description. A converged run emits nothing, because undoing a run that changed nothing means doing nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.block.remove`
- `file.line.set`
- `file.copy`
- `file.permissions`

## Examples

Manage a hosts file entry:

```yaml
- name: Keep the cluster's short names resolvable
  file.block.set:
    path: /etc/hosts
    block: |
      10.0.0.11 db1
      10.0.0.12 db2
```

Use a marker a file's own syntax allows:

```yaml
- name: Manage the sshd hardening stanza
  file.block.set:
    path: /etc/ssh/sshd_config
    marker: "# {mark} PLEIADES HARDENING"
    block: |
      PermitRootLogin no
      PasswordAuthentication no
```

Keep two independent blocks in one file:

```yaml
- name: Manage the proxy stanza only
  file.block.set:
    path: /etc/environment
    marker_begin: OPEN PROXY
    marker_end: CLOSE PROXY
    block: |
      http_proxy=http://proxy.internal:3128
```


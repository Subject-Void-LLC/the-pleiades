---
status: beta
---

# file.block.remove

Removes a marked, multi-line block of text from a file.

Takes away the region a file.block.set task left in a file, the two marker lines included, and leaves every other line alone. A file with no such markers is already in the state this asks for, so the task reports no change and writes nothing. The file must already exist, since a path that is not there is far more often a typo than a file whose block is missing; a symbolic link is refused, because writing the file back would replace the link with a regular file. Its mode, owner and group are put back after the rewrite. Markers that do not pair up (two end markers, or an end with no begin) are an error rather than a guess about which lines to delete, which matters more here than anywhere else in this namespace: the guess would be a deletion.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The file to edit. It must already exist. |
| `marker` | `string` | no | `# {mark} ANSIBLE MANAGED BLOCK` | The template for both marker lines, which must be the one the block was written with. The {mark} placeholder is replaced by marker_begin on the line above the block and by marker_end on the line below it. |
| `marker_begin` | `string` | no | `BEGIN` | The word {mark} becomes on the line above the block. |
| `marker_end` | `string` | no | `END` | The word {mark} becomes on the line below the block. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file this task acted on. |
| `present` | `bool` | always | Whether a marked block is in the file now, read back from the device. False after a successful run. |
| `block` | `string` | always | The text between the markers now, which is empty once the block is gone. |
| `diff` | `dict` | always | The before and after state of the marked region, each holding whether the block was present and what was between the markers. The before half is where the removed text is recorded. Recorded even when nothing changed, in which case the two halves are identical. |
| `inverse` | `dict` | when the run removed a block | The task that undoes this run: a file.block.set carrying the body that was removed. A run that found no block records none, which is how the journal says undoing it means doing nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Deleting text is usually the least reversible thing a method can do, and the markers are what make this the exception: they say exactly which lines belonged to the block, so the run that removes them captures the body and emits a file.block.set that writes it back. file.line is weaker because it identifies its target by a pattern rather than by a boundary, so it cannot be sure which of several matching lines it took away. What this still does not restore: a block that was in the MIDDLE of a file comes back at the END, because file.block.set appends, and the emitted inverse says so in its own description when that applies. Nor does it restore a final newline added to a file that had none. A run that found no block emits nothing, because undoing a run that changed nothing means doing nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.block.set`
- `file.line.remove`
- `file.remove`

## Examples

Stop managing a hosts file entry:

```yaml
- name: Drop the cluster short names
  file.block.remove:
    path: /etc/hosts
```

Remove a block written with its own marker:

```yaml
- name: Retire the hardening stanza
  file.block.remove:
    path: /etc/ssh/sshd_config
    marker: "# {mark} PLEIADES HARDENING"
```


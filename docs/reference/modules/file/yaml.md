---
status: beta
---

# file.yaml

Sets, or removes, the value at a key path in a YAML file on the device, keeping its comments and the rest of the file.

Reads the file (at most 4 MiB of UTF-8, one document; refused when it holds a duplicate key) and sets the value at the key path, or removes it with state absent, keeping comments and key order, and a replaced value's own comments. The rest of the document is written back in go.yaml.in/yaml/v3's style at the indent the file used, so quoting and spacing may be normalized; it is not a byte-for-byte edit. A path through an alias is refused, since one edit would change every place the alias is used. A key already holding the value is no change and nothing is written. The write keeps the file's owner, group and mode. The undo writes back the whole earlier text, withheld from the journal; a file the run created is removed. A check makes the same edit, reports the key's value before and after, and writes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `path` | `string` | yes | - | The YAML file on the device, an absolute path. |
| `key` | `string` | yes | - | The key path: dotted (server.port, items.0.name, a whole number indexing a list), or a list of segments ([server, "a.b", 0]) for a key holding a dot. A missing key is created, with a mapping for each missing step; an index one past a list's end appends. |
| `value` | `any` | no | - | The value to set, any YAML value (a mapping, a list, a scalar, null). Required with state present. |
| `state` | `string` | no | `present` | present sets the value; absent removes the key, which is no change when it is not there. |
| `create` | `bool` | no | `false` | Start from an empty document when the file does not exist; without it a missing file is refused. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file. |
| `key` | `string` | always | The key path, dotted. |
| `created` | `bool` | always | Whether the run created the file (or, in a check, would). |
| `diff` | `dict` | always | The key's value before and after, as the format writes it; empty where there is none. Masked as every diff is. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed an existing file emits a file.copy writing back the whole text it held, with its mode, owner and group; that text is withheld from the journal, so a rollback replays it only from the task's own rollback: list. A run that created the file emits a file.remove. A run that changed nothing emits nothing. The modification time is not restored.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.json`
- `file.line`

## Examples

Raise the replica count:

```yaml
- name: Replicas
  file.yaml:
    path: /srv/app/values.yaml
    key: web.replicas
    value: 3
```


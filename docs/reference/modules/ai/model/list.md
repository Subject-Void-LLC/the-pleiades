---
status: beta
---

# ai.model.list

Lists the models an Ollama host has on disk and the ones in memory.

Asks the Ollama host for its models (GET /api/tags) and the ones loaded in memory (GET /api/ps). It only reads, and needs no host guard, so a check of it is the task itself.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `AIProviderCapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `models` | `list of dict` | always | Each model on disk: name (with its tag), size in bytes and digest. |
| `resident` | `list of string` | always | The models in memory. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It asks the host which models it has on disk and in memory, and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.model.load`
- `ai.provider.list`

## Examples

What the workstation has:

```yaml
- name: Models
  ai.model.list: {}
```


---
status: beta
---

# ai.model.unload

Unloads a model from an Ollama host's memory.

Unloads the model (keep_alive 0); a model not in memory is no change. The undo loads it again. A check reports whether it is in memory. It runs only against an ai_provider whose kind is ollama and whose record declares ai_host_guard: single_resident, and keeps to it: at most one model in memory. It speaks only Ollama's HTTP API, so it never restarts, stops or signals the host, or the machine or distribution it runs in.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `AIProviderCapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `model` | `string` | yes | - | The model, as Ollama names it; a name with no tag means :latest. |
| `timeout` | `string` | no | `10m` | How long the change may take, from 1s to 6h. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `model` | `string` | always | The model, with its tag. |
| `resident_before` | `list of string` | always | The models in memory before. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

The undo loads the model again, which unloads any other loaded since.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.model.load`

## Examples

Free the memory:

```yaml
- name: Unload
  hosts: workstation
  ai.model.unload:
    model: "qwen3:14b"
```


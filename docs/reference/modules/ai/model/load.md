---
status: beta
---

# ai.model.load

Loads a model into an Ollama host's memory, unloading every other model first so one is resident at a time.

Loads the model to stay in memory (keep_alive -1), after unloading every other resident model, so the host never holds two; a model already alone in memory is no change. The model must be on the host already (ai.model.pull). The undo loads the model that was resident before, or unloads this one when none was. A check reports what is in memory and what would change. It runs only against an ai_provider whose kind is ollama and whose record declares ai_host_guard: single_resident, and keeps to it: at most one model in memory. It speaks only Ollama's HTTP API, so it never restarts, stops or signals the host, or the machine or distribution it runs in.

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
| `unloaded` | `list of string` | always | The models unloaded (or, in a check, that would be). |
| `loaded` | `bool` | always | Whether the model was loaded (or would be). |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

The undo loads the model that was in memory before, which unloads this one; when none was, it unloads this one. It restores which model is resident, not how long the host had kept it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.model.unload`
- `ai.model.pull`

## Examples

Make one model resident:

```yaml
- name: Load
  hosts: workstation
  ai.model.load:
    model: "qwen3:14b"
```


---
status: beta
---

# ai.provider.list

Reports what a provider is and which models it serves: kind, endpoint, residency, prices and host guard.

Reads the ai_provider device's record (its kind, API base URL, residency, price table and host guard) and asks the provider for the models it serves, with the device's own key over its own TLS: a listing no provider counts as a call. A kind needing a key the device does not have is refused before contact. It only reads, so a check of it is the task itself.

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
| `kind` | `string` | always | The provider's API: ollama, anthropic, google, openai, or a kind an external Collection registered. |
| `api_base` | `string` | always | The API's base URL. |
| `residency` | `string` | always | local or hosted, as the record says. |
| `models` | `list of string` | always | The models the provider serves, sorted. |
| `prices` | `dict` | always | The record's price table: model to input and output price per million tokens, and currency. |
| `host_guard` | `string` | always | The host guard the record declares (single_resident), or empty. |
| `warnings` | `list of string` | when there is one | What the record allows that a run must say: a key over plain http://, weakened TLS. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the provider's record and asks it which models it serves, which no provider counts as a call.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.provider.probe`
- `ai.model.list`

## Examples

List every provider:

```yaml
- name: Providers
  hosts: [claude, workstation]
  ai.provider.list: {}
```


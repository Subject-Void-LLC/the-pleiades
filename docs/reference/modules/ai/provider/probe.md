---
status: beta
---

# ai.provider.probe

Makes one counted call of a few tokens to a provider's model and reports its latency and usage.

Asks the model for a one-word answer, at most 8 tokens at temperature 0 and with no retry, so a failure is the provider's answer as it stands: proof that the key, the endpoint, the TLS and the model all work, and what one call costs in time. The call is counted, its usage record returned and, with usage_sink, appended to the project. A check makes no call and reports the call it would make, with its estimate.

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
| `model` | `string` | yes | - | The model to ask for, as the provider names it. |
| `timeout` | `string` | no | `5m` | How long one call may take, retries included, from 1s to 1h. |
| `usage_sink` | `string` | no | - | A file name under the project's ai/usage directory (spend.jsonl) each call's usage record is appended to as one JSON line, proved writable before any call. Crawl tier only. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `latency_ms` | `int` | success | How long the call took, in milliseconds. |
| `answered_by` | `string` | success | The model the provider says answered. |
| `usage` | `dict` | success | The call's usage record, as ai.chat returns one. |
| `estimate` | `dict` | in a check | The call a run would make, and what it would cost. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A probe is a counted call: the provider has seen it and billed it, and nothing on a device changed that could be put back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.provider.list`
- `ai.chat`

## Examples

Prove a provider answers:

```yaml
- name: Probe
  ai.provider.probe:
    model: claude-haiku-5-5
```


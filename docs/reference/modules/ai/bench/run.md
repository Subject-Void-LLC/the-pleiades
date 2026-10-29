---
status: beta
---

# ai.bench.run

Asks a provider's model a prompt set, scores each answer, and reports the score with every call's tokens, time and cost.

Asks the target provider's model each prompt of the set in turn, as ai.chat asks one, and scores each answer with the prompt's scorer; a call that fails counts as failed and the set goes on. It returns each prompt's result (passed, tokens, time, cost, and the error of a failed call), how many passed, the score, and the total cost when every call is priced. Every call's usage record is appended to usage_sink, and with save_as the results, with the answers, are saved under the project's ai/bench. A check makes no call: it validates the set and its scorers and estimates its tokens.

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
| `prompts` | `list of dict` | yes | - | The prompt set, up to 200: each {id, text or messages, expect}, where expect is exact (the answer, trimmed, equals it), contains, regex (RE2) or json_field ({path, equals}: the answer read as JSON holds equals at path, a dotted list of keys). |
| `save_as` | `string` | no | - | A file name under the project's ai/bench (run1.json) the results are saved to, with each answer cut to 4 KiB; never written over. Crawl tier only. |
| `residency` | `string` | no | - | The use's residency. local refuses a provider whose record says hosted, before contact and at validate; hosted admits either. **Choices:** `local`, `hosted`. |
| `temperature` | `float` | no | - | Sampling temperature, 0 to 2. A kind that does not accept a parameter reports it ignored and does not send it. |
| `top_p` | `float` | no | - | Nucleus sampling, 0 to 1. |
| `max_tokens` | `int` | no | - | The most tokens the answer may take, 1 to 1000000. A check prices the answer at this many; without it the answer's cost is unbounded. |
| `stop` | `list of string` | no | - | A sequence, or a list of up to 8, that ends the answer. |
| `thinking` | `int` | no | - | A token budget for a model that thinks before it answers (Ollama turns thinking on); a kind without thinking ignores it. |
| `schema` | `dict` | no | - | A JSON schema the answer must meet, asked for through the kind's own structured output and checked before the task succeeds. It keeps to type, properties, required, additionalProperties, items, enum, const, minimum, maximum, minLength, maxLength, minItems and maxItems; any other keyword is refused rather than half-checked. |
| `timeout` | `string` | no | `5m` | How long one call may take, retries included, from 1s to 1h. |
| `retries` | `int` | no | `3` | How many times a retryable failure (a rate limit, an overloaded or failing server, a provider never reached) is tried again, with exponential backoff and jitter and honoring Retry-After; 0 to 10. Nothing else is retried, and nothing after the answer began. |
| `usage_sink` | `string` | no | - | A file name under the project's ai/usage directory (spend.jsonl) each call's usage record is appended to as one JSON line, proved writable before any call. Crawl tier only. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `results` | `list of dict` | success | Each prompt's id, passed, input and output tokens, duration, cost when priced, and error when its call failed. |
| `passed` | `int` | success | How many prompts passed. |
| `count` | `int` | always | How many prompts the set holds. |
| `score` | `float` | success | passed divided by count. |
| `provider` | `string` | success | The provider device the set was asked of. |
| `model` | `string` | success | The model that answered, as the task named it. |
| `cost` | `float` | when every call is priced | What the set cost, in currency. |
| `currency` | `string` | when every call is priced | The price table's currency. |
| `saved` | `string` | with save_as | The results file, relative to the project. |
| `estimate` | `dict` | in a check | The set's estimated input tokens, and each prompt's estimate. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Each prompt is a counted call the provider has seen and billed; nothing on a device changes. A saved result is a new file under the project's ai/bench, never written over, which the run reports and does not remove.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.chat`
- `ai.provider.probe`

## Examples

Score a local model:

```yaml
- name: Bench
  ai.bench.run:
    model: "qwen3:14b"
    max_tokens: 50
    prompts:
      - {id: capital, text: "Capital of France? One word.", expect: {contains: Paris}}
      - {id: label, text: "Answer {\"label\": \"bug\"} as JSON.", expect: {json_field: {path: label, equals: bug}}}
    save_as: qwen3-14b.json
```


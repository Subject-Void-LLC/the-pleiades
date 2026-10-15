---
status: beta
---

# ai.chat

Asks the target provider's model and returns its answer with a usage record: tokens, time, retries and cost.

Sends messages, or a prompt bundle with optional untrusted input, to the model named on the ai_provider device the task targets, through its kind (ollama, anthropic, google, or any server speaking OpenAI's chat completions API), with the device's own key over its own TLS and no redirect followed. Before any call it refuses a use tagged local against a hosted provider, a kind needing a key the device does not have, and a bundle its signing policy does not admit; a parameter the kind does not accept is reported ignored and not sent, and one the provider rejects (a 400 naming it) is reported dropped, the call sent once more without it, and never sent again to that provider for that model in the run. Only rate limits, overload, server errors and a provider never reached are retried, with exponential backoff and jitter and honoring Retry-After, and nothing after the answer began streaming, which could bill twice. With schema the answer is asked for through the kind's structured output and checked against the schema before the task succeeds. Every call ends in a usage record, returned and, with usage_sink, appended to a file in the project; the messages and the answer are never in it. With review rules, an answer crossing a threshold is held for a person before any later task can read it: asked at the terminal when someone is there, otherwise kept in the project's ai/review with the task failing as held until it is decided with pleiades ai review, after which running again reuses the decision with no second call; with nobody to ask at all it fails closed. A signature says who approved a bundle's text and that it is unchanged; it is provenance and integrity, not a promise about what the model does with it. A check makes no call: it validates the key, the residency, the parameters against the kind, the schema and the bundle's signature, and estimates the cost from the prompt's size and max_tokens. It reports changed, since a real run makes a counted call.

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
| `messages` | `list of dict` | no | - | The conversation: a list of {role, content}, role system, user or assistant, with at least one user message. One of messages, prompt and prompt_file. |
| `prompt` | `dict` | no | - | A prompt bundle given inline: {kind: prompt, name, version, use, text, variants, parameters, expires}, exactly the document its signature covers. One of messages, prompt and prompt_file. |
| `prompt_file` | `string` | no | - | A prompt bundle in the project by name: ai/prompts/<name>.json, with its signature, if any, in <name>.json.sig beside it. Crawl tier only. |
| `signature` | `dict` | no | - | An inline prompt's detached signature, {key, sig}, as pleiades ai prompt sign writes it. |
| `input` | `string` | no | - | Untrusted text for a prompt bundle (a ticket, a file, an earlier answer), placed after the bundle's text between fixed markers as one JSON string, so nothing in it can close the bundle's instructions. The model still reads it: a signature vouches for the bundle, not for what the model does with its input. |
| `signing` | `string` | no | `optional` | The use's signing policy. off checks nothing; optional checks a signature when there is one and records an unsigned bundle as unsigned, never as verified; required refuses an unsigned, invalid, expired, unknown-key, wrong-role or rolled-back bundle before any call. |
| `residency` | `string` | no | - | The use's residency. local refuses a provider whose record says hosted, before contact and at validate; hosted admits either. |
| `review` | `list of dict` | no | - | Review rules (human in the loop): thresholds on the answer, any one of which holds it for a person's decision before a later task can read it. Each is one of cost_over (a number in the price table's currency; an unpriced call holds), output_tokens_over, answer_matches (RE2), field (a dotted path in a JSON answer, with below, above, equals, in or not_in; a missing field holds), stop_reason (a list), fallback (ai.route), unsigned and always. A person at the terminal is asked; with nobody there the answer is written to the project's ai/review and the task fails as held until `pleiades ai review approve` or `deny`, and running again reuses the decision with no second call. With nobody to ask at all (the Walk tier), a held answer fails closed. |
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
| `answer` | `string` | success | The model's answer, whole: every kind streams, and the task sees one answer. |
| `json` | `any` | when schema is set | The answer read as JSON, once it met the schema. |
| `stop_reason` | `string` | success | Why the model stopped, in the provider's own words. |
| `usage` | `dict` | success | The usage record: time, method, provider, kind, residency, model asked for and the model that answered, input and output tokens, duration, retries, the parameters ignored and dropped, the cost and currency when the provider's price table prices the model, the prompt's verdict, and the outcome. It never holds what was sent or answered. |
| `prompt` | `dict` | always | The prompt's verdict: status (verified, unsigned, not checked) and, for a bundle, its name, version, SHA-256 and signing key. |
| `ignored` | `list of string` | success | Parameters the provider's kind does not accept, which were not sent. |
| `dropped` | `list of string` | success | Parameters the provider rejected, removed and never sent to it for that model again in the run. |
| `estimate` | `dict` | in a check | What a check reports instead of a call: the prompt's tokens (four characters a token), max_output_tokens or output_unbounded, the parameters that would be ignored and dropped, and max_cost with its currency when the model is priced. |
| `warnings` | `list of string` | when there is one | What the provider's record allows that a run must say: a key sent over plain http://, weakened TLS. |
| `review` | `dict` | with review rules | The answer's review: not held, or approved with the key, the rules it matched, who decided and when; in a check, the rules and any decision already on record. A held answer is never returned: the task fails as held. |
| `reused` | `bool` | when a decided answer is reused | True when the answer was approved earlier for this very request and returned from its review with no call; usage is the original call's. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A call is made and cannot be unmade: the provider has seen what was sent and counted what it answered. Nothing on a device changes, so there is nothing a later task could put back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.route`
- `ai.prompt.verify`
- `ai.provider.probe`

## Examples

Ask a local model for a structured answer:

```yaml
- name: Classify
  ai.chat:
    model: qwen3:14b
    residency: local
    messages:
      - {role: user, content: "Is 'disk full on /var' a bug or an outage? Answer as JSON."}
    schema:
      type: object
      properties: {label: {type: string, enum: [bug, outage]}}
      required: [label]
    max_tokens: 200
    usage_sink: spend.jsonl
  register: verdict
```

Send a signed prompt with untrusted input:

```yaml
- name: Triage
  ai.chat:
    model: claude-haiku-5-5
    prompt_file: triage
    signing: required
    input: "{{ vars.ticket_text }}"
```


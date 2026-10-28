---
status: beta
---

# ai.route

Asks a use's providers in fallback order and returns the first answer, refusing any provider the use's residency forbids.

Routes a call by use: the routing table names, for each use, its residency (local or hosted), its signing policy and its providers in fallback order, each an ai_provider device with the model to ask it for. Before any call it resolves every provider in the use's order (each must be an onboarded ai_provider, with a key when its kind needs one) and refuses the route as a whole when one would take a local use's data to a hosted provider, which pleiades validate refuses already for a literal table. The use, not the task, decides the signing policy and the residency, so the task may set neither, nor model; a prompt bundle must be for the routed use. It then calls the providers in order, as ai.chat calls one, and moves to the next only when a provider could not be reached or kept failing in a way only time mends (rate limits, overload, server errors) after its retries, before any answer began; a refused request, a refused key or an answer missing its schema fails the task instead, since the next provider would be asked the same wrong thing or hide a broken credential. Every attempt's usage record is appended to usage_sink. It runs once, with no target device (no hosts:), and reaches providers through the project, so it runs on the Crawl tier; on the Walk tier target the provider and use ai.chat. A check makes no call: it resolves the route and estimates each provider's cost.

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | - |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on no device, so a task skips the runbook's hosts: and runs once |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `use` | `string` | yes | - | The use to route: a key of routes. |
| `routes` | `dict` | yes | - | The routing table: each use's residency (local or hosted, required), signing policy, review rules (as review takes them; a task's add to the use's), and providers in fallback order, as {provider: <ai_provider device>, model}. A literal table is checked against the inventory at validate. |
| `review` | `list of dict` | no | - | Review rules (human in the loop): thresholds on the answer, any of which holds it for a person's decision before a later task can read it. Each names a single threshold: cost_over (a number in the price table's currency; an unpriced call holds), output_tokens_over, answer_matches (RE2), field (a dotted path in a JSON answer, with below, above, equals, in or not_in; a missing field holds), stop_reason (a list), fallback (ai.route), unsigned and always. A person at the terminal is asked; with nobody there the answer is written to the project's ai/review and the task fails as held until `pleiades ai review approve` or `deny`, and running again reuses the decision with no second call. With nobody to ask at all (the Walk tier), a held answer fails closed. |
| `messages` | `list of dict` | no | - | The conversation: a list of {role, content}, role system, user or assistant, with at least one user message. Give exactly one: messages, prompt or prompt_file. |
| `prompt` | `dict` | no | - | A prompt bundle given inline: {kind: prompt, name, version, use, text, variants, parameters, expires}, exactly the document its signature covers. Give exactly one: messages, prompt or prompt_file. |
| `prompt_file` | `string` | no | - | A prompt bundle in the project by name: ai/prompts/<name>.json, with its signature, if any, in <name>.json.sig beside it. Crawl tier only. |
| `signature` | `dict` | no | - | An inline prompt's detached signature, {key, sig}, as pleiades ai prompt sign writes it. |
| `input` | `string` | no | - | Untrusted text for a prompt bundle (a ticket, a file, an earlier answer), placed after the bundle's text between fixed markers as one JSON string, so nothing in it can close the bundle's instructions. The model still reads it: a signature vouches for the bundle, not for what the model does with its input. |
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
| `estimate` | `dict` | in a check | What a check reports instead of a call: the prompt's tokens (four characters a token), max_output_tokens or output_unbounded, the parameters that would be ignored and dropped, and, when the model is priced, input_cost, and max_cost when the output has a cap that is sent, with the currency. |
| `warnings` | `list of string` | when there is one | What the provider's record allows that a run must say: a key sent over plain http://, weakened TLS. |
| `review` | `dict` | with review rules | The answer's review: not held, or approved with the key, the rules it matched, who decided and when; in a check, the rules and any decision already on record. A held answer is never returned: the task fails as held. |
| `reused` | `bool` | when a decided answer is reused | True when the answer was approved earlier for this very request and returned from its review with no call; usage is the original call's. |
| `provider` | `string` | success | The provider that answered. |
| `model` | `string` | success | The model it was asked for. |
| `use` | `string` | always | The use routed. |
| `attempts` | `list of dict` | success | Each provider tried, in order, with its model, outcome and, for a failure, why. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A call is made and cannot be unmade: the provider has seen what was sent and counted what it answered. Nothing on a device changes, so there is nothing a later task could put back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.chat`
- `ai.provider.list`

## Examples

Summarize locally, falling back to a second local server:

```yaml
- name: Summarize
  ai.route:
    use: summarize
    routes:
      summarize:
        residency: local
        signing: optional
        providers:
          - {provider: workstation, model: "qwen3:14b"}
          - {provider: lab-vllm, model: qwen3-14b}
    messages:
      - {role: user, content: "Summarize the release notes in three lines."}
    max_tokens: 300
```


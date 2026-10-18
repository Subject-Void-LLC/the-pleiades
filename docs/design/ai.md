# Design note: the `ai` collection

Status: built on the Crawl tier, with the limits named under "The Walk tier". It covers calling language models
as a step of a run, from a local Ollama, Anthropic, Google, or anything that speaks the OpenAI chat API:
- routed by use, with data residency enforced before a call is made;
- metered per call;
- held for a person's decision when the answer crosses a threshold the use sets;
- with prompts that can be signed, so a run can say which prompt it sent and who vouched for it.

## Providers are devices

A provider is inventory data: an `ai_provider` device. Its record says:
- its **kind**: `ollama`, `anthropic`, `google`, `openai`, or a kind an external Collection registers;
- its **endpoint**: `api_base`;
- its **residency**: `local` or `hosted`, required, with no default;
- optionally, a **price table**: `ai_prices`, as `model=input/output` per million tokens, with
  `ai_price_currency`;
- for an Ollama host, its **host guard**: `ai_host_guard: single_resident`.

The provider's API key is the device's own credential, stored like any other
(`pleiades add-credential claude --token-stdin`). A template's machine credential never becomes one
(`record.RegisterOwnCredentialType`). The key is sent only to the provider's own endpoint, under `pkg/httpapi`'s
rules: no redirect is followed, and plain http:// carries a key only when the record allows it by name. A hosted
provider is reached over https only, because what it is sent leaves the operator's hardware. Its TLS is
`pkg/devicetls`'s.

A provider starts `discovered`. Onboarding lists its models with its key; the listing costs nothing, so
onboarding is never billed.

**Kinds are a registry** (`pkg/aiprovider`, public). Each kind builds its own requests and reads its own answers
behind one interface: the parameters it accepts, a streamed chat, a model list, and which errors are worth
retrying. The four built-in kinds register in `pkg/aiprovider`'s init. An external Collection, which can import
`pkg/`, registers another kind in its own program. It builds its methods on the same interface and on
`pkg/aitask` (a task's parameters, prompts, signatures, residency, review and usage), so its calls are metered,
retried, held and refused the same way. The `openai` kind covers every server that speaks
`/v1/chat/completions` (Alibaba, Mistral, Groq, xAI, vLLM, LM Studio), and takes no key unless one is stored.

## Methods

| Method | Does | Site, device |
|---|---|---|
| `ai.chat` | Sends messages, or a prompt bundle with untrusted input, to the target provider's model, optionally asking for a JSON-schema answer. Returns the answer and a **usage record**. | controller, required |
| `ai.route` | Calls a **use**'s providers from a routing table, in fallback order, and returns the first answer. | controller, none |
| `ai.provider.list` | Reports what a provider is (kind, endpoint, residency, prices, guard) and which models it serves. | controller, required |
| `ai.provider.probe` | Makes one counted call of at most 8 tokens and reports latency and usage. | controller, required |
| `ai.model.list` / `load` / `unload` / `pull` / `remove` | An Ollama host's models: list, load (unloading every other first), unload, pull, remove. | controller, required |
| `ai.bench.run` | Asks a prompt set, scores each answer (exact, contains, regex, JSON field), and saves counted results. | controller, required |
| `ai.prompt.verify` | Verifies a signed prompt, parameter set or routing table against the trusted keys. | controller, none |

More on each:
- **`ai.route`** resolves every provider and holds it to the use's residency before the first call. It moves on
  only when a provider was unreachable, or kept failing in a way only time mends.
- **`ai.model.*`**: every method but `list` needs the device's declared host guard. `remove` exists so that a pull
  has an undo.

Two CLI commands are not runbook methods, because each needs a person:
- `pleiades ai prompt sign` (and `keygen`), because signing needs a private key;
- `pleiades ai review`, because approving or denying a held answer is the person's decision.

Both refuse without a terminal and under an agent. The Walk tier has neither.

## A chat call

- **Parameters.** `temperature`, `top_p`, `max_tokens`, `stop`, `thinking` (a token budget), `schema`,
  `timeout`, `retries`.
  - A parameter the kind does not accept is reported `ignored` and not sent.
  - One the provider rejects at run time (a 400 naming it) is reported `dropped`, removed, and the call sent once
    more without it. The run remembers it (`sdk.AIProject.AIDrop`), so it is never sent to that provider for that
    model again in the run.
  - Structured output uses the kind's own mechanism: Anthropic's tool input schema, Google's `responseSchema`,
    OpenAI's `response_format`, Ollama's `format`. The answer is checked against the schema before the task
    succeeds. The schema validator is a subset, and a keyword outside it is refused rather than half-checked.
- **Retries.** Only what is retryable is tried again: 408, 429, 500, 502, 503, 504, 529, or a provider never
  reached. Waits use exponential backoff with jitter and honor `Retry-After`, up to `retries` (default 3). Nothing
  else is retried, and nothing after an answer began streaming, which could bill twice.
- **Streaming.** Every kind streams (SSE, or Ollama's NDJSON), so a long generation keeps bytes moving through any
  idle timeout on the way. The task sees one answer, capped at 16 MiB.
- **Usage.** Every call ends in a usage record, on failure too. It holds:
  - the time, method, provider, kind and residency;
  - the model asked for and the model that answered;
  - tokens in and out, duration and retries;
  - the parameters ignored and dropped;
  - the cost and currency, when the price table prices the model (by the model that answered, else the one asked
    for);
  - the prompt's verdict, the review's outcome, and whether the call succeeded.

  With `usage_sink` (a file name under the project's `ai/usage`), each record is appended as one JSON line. The
  sink is proved writable before the call, so a call is never made whose record could not be kept.
- **What is recorded.** The answer is a return, as every method's output is. The usage record never holds what
  was sent or answered: a prompt can hold data a run should not keep. A prompt bundle is recorded by name,
  version and hash.

## Uses, residency and routing

A **use** names a purpose (`triage`, `summarize`). It has:
- a residency (`local` or `hosted`);
- a signing policy;
- review rules;
- its providers in fallback order, each with the model to ask for.

The routing table is a mapping of uses given to `ai.route`, literally or from a run variable, so a project can
keep one table. **The use, not the task, decides** residency, signing and model, so a task may set none of them.
A task's review rules can only add to the use's.

**A use tagged `local` is refused against a `hosted` provider before contact.** `pleiades validate` checks every
literal table against the inventory's residencies, and also `ai.chat`'s `residency` parameter against every
device the task targets. At run time, every provider in a use's order is resolved and checked before the first
call, so a table rendered from a variable is held to the same rule.

`ai.route` falls back only when a provider was unreachable, or failed retryably and ran out of retries, before
any answer began. A refused request, a refused key, or an answer missing its schema fails the task instead: the
next provider would be asked the same wrong thing, or would hide a broken credential.

## Human review (human in the loop)

A use, or a task, can name **review rules**: thresholds on the answer, any one of which holds it for a person's
decision before any later task can see it.

```yaml
review:
  - cost_over: 0.50                            # in the price table's currency; an unpriced call holds
  - output_tokens_over: 2000
  - answer_matches: "(?i)drop table|rm -rf"    # RE2
  - field: confidence                          # a dotted path in a JSON answer
    below: 0.8                                 # or above, equals, in, not_in; a missing field holds
  - stop_reason: [max_tokens, length]          # the provider's own words
  - fallback: true                             # ai.route: an answer from other than the first provider
  - unsigned: true                             # the prompt was not verified
  - always: true
```

Each matching rule is named in the review. A rule that cannot be evaluated (cost with no price, a field in an
answer that is not JSON) **holds**: a threshold that cannot be checked is not passed. A held answer is released
only by a person's decision:

- **A person at the terminal** (Crawl; standard input is a terminal; no agent detected). The CLI prints the
  review, escaped for the terminal: the answer, the rules that matched, and the cost. It asks to approve or deny,
  one review at a time across a fanned-out task.
- **Nobody at the terminal** (unattended, scheduled, piped, or an agent caller). The review is written to the
  project as `ai/review/<request key>.json` and the task fails with status `held`, naming the command:
  - `pleiades ai review list`, `show <key>`, `approve <key>`, `deny <key>`;
  - approve and deny refuse without a terminal and under an agent, so an AI never approves its own output;
  - the approval binds to the SHA-256 of the answer the person was shown.
- **The request key** is the SHA-256 of the call as asked: method, providers and models, messages, parameters,
  schema and rules. A run that asks the same thing again finds the review before calling:
  - approved: it reuses the approved answer with no call, so nothing is billed twice;
  - denied: it fails, naming who denied it and when;
  - still pending: it fails again with no call.

  An approved answer whose bytes no longer hash to what was approved is refused.
- **A check** makes no call, so it cannot evaluate a rule. It validates the rules, and reports a decision already
  on record for the request.

**What this protects against, stated plainly.** The decision gate stops an unattended run, or an agent driving the
CLI, from releasing an answer past a threshold. A runbook cannot approve itself either: a task writes only under
`ai/usage` and `ai/bench`, never `ai/review`. On the Crawl tier a decision is a file in the project, so it is not
a cryptographic barrier against a process running with the operator's own file access. Approver identity and
roles are the Controller's job (Phase 27).

### Pause and resume: the seam Phase 27 fills

Holding should pause the run: persist what has finished and the node awaiting a decision, end the process, and
resume from that node once the decision arrives, possibly days later. The engine cannot do that yet. A run is one
process holding its DAG in memory, so a held node **fails the run closed** with status `held`, and the decision is
applied by running it again. The `ai` methods make that safe by reusing the decided answer rather than calling
again.

What is declared now so Phase 27 builds on it rather than around it (`internal/engine/pause.go`):
- `engine.Hold`, the typed hold a node's error carries (`sdk.HeldError` from a method);
- `engine.PausedRun` and `engine.HeldNode`, the record a paused run would be;
- `engine.PauseStore`, the store that would keep one;
- `engine.Decision`;
- `Executor.Resume`, which today returns `ErrPauseNotBuilt`.

No option wires a store in, so nothing can configure a pause that silently does nothing. The Walk tier's
`workflow-approvals` view (declared) is where those decisions will be made, with an approver role.

## Signing

A **bundle** is canonical JSON of `{kind, name, version, use, expires, text, variants (per model), parameters,
body}`, with a detached Ed25519 signature (`crypto/ed25519` alone).
- `kind` is `prompt`, `parameters` or `routes`.
- **Canonical** means: UTF-8, object keys sorted by code point, no white space between tokens, strings escaped
  minimally, and numbers as Go writes a float64 at its shortest.
- It **refuses**, rather than resolves, what two readers could read differently: a duplicate key at any depth,
  invalid UTF-8, an unpaired surrogate, a number with an exponent or beyond 2^53, a second document, and nesting
  past 64.
- The canonical form has test vectors and a fuzz test.

A bundle's signed `parameters` apply to the call, and a task may not override one. A per-model variant replaces
the text for that model. **Untrusted text** (a ticket, a file, an earlier answer) is given as `input`. It is
placed after the bundle's text between fixed markers, as one JSON string. A JSON string holds no raw line break,
so nothing in it can close the bundle's instructions, and `FuzzDelimit` holds that. The model still reads the
input. A signature vouches for the bundle, not for what the model does with its input.

**Trusted keys** are public keys, each with a **role** listing the uses it may sign for, and an optional expiry.
They live in the project's `ai/trusted-keys.yaml`. `ai.chat`, `ai.route` and `ai.prompt.verify` check, in order:
1. the bundle parses canonically;
2. a trusted key verifies the signature;
3. the key's role lists the bundle's use;
4. neither the key nor the bundle has expired;
5. its version is not older than the newest of its name already used (`.pleiades/prompt-versions/`).

A version is recorded as used only once a real call is decided, never by a check or by `verify`.

A use's **policy** is one of three:
- `off` checks nothing.
- `optional` (the default) checks a signature that is present, and records an unsigned bundle as `unsigned`,
  never `verified`.
- `required` fails closed on a missing, invalid, expired, unknown-key, wrong-role or rolled-back bundle.

A run with no trusted keys refuses a signature it cannot check, rather than calling the key unknown.

A signature is **provenance and integrity, not safety**. It says who approved a prompt's text and that it is
unchanged, not that what the model does with it is safe.

## Check mode

No model is called by any method's check.
- `ai.chat` and `ai.route` check:
  - the key (a kind that needs one has one);
  - residency;
  - the parameters against the kind (`ignored`, and `dropped` earlier in the run);
  - the schema, the review rules and the bundle's signature;
  - the sink.

  They then estimate cost: four characters a token, with `max_tokens` out, against the price table. Without
  `max_tokens`, the estimate says the answer's cost is unbounded.
- `ai.provider.list` and `ai.model.list` only read, so their check is the task.
- `probe` and `bench` estimate.
- The model changes report what is on the host and what would change.

The methods that make a counted call report `changed`, so a check shows them as `would_change`.

## Capabilities

`ai_provider` declares `NetworkAddressable` and, once onboarded, `AIProviderCapable`. That capability is reached
over `https` and carries the kind, endpoint, residency, prices, guard and plaintext allowance. Every
device-targeting method requires it, and the model methods also require the kind `ollama`. `ai.route` and
`ai.prompt.verify` take no device.

## Check and Reversibility

| Method | Check | Reversible |
|---|---|---|
| `ai.chat`, `ai.route` | No call: validation and a cost estimate. | No: a call is made and cannot be unmade. Nothing on a device changes. |
| `ai.provider.list`, `ai.model.list`, `ai.prompt.verify` | Same as Invoke. | Read-only. |
| `ai.provider.probe`, `ai.bench.run` | No call; an estimate. | No: counted calls. |
| `ai.model.load` / `unload` | Reports what is resident and what would change. | Yes. Each is the other's undo: `load`'s undo loads the model resident before it, or unloads this one. |
| `ai.model.pull` / `remove` | Reports whether the model is present. | Yes. Each is the other's undo, and a pull's undo is recorded only when the pull added the model. |

## Decisions, by edge case

- **A provider is a device, not a connection.** Every provider's key is its own device's credential.
  - `ai.chat` calls the task's target.
  - `ai.route` runs once with no target, and reaches the providers its table names through the project
    (`sdk.AIProject.AIProvider`). That hands out an onboarded `ai_provider` with its own credential and nothing
    else, so a routing table can never yield a machine's login.
  - Choosing a provider in one task and calling it in a second was rejected. A fanned-out task cannot choose
    across devices, and a provider that fails at the call could not be fallen back from.
- **Residency is checked before contact, at validate where the table is literal.** A use tagged local that sends
  data to a hosted provider is the failure this exists to prevent. A refusal after half a run has happened is
  worse than one before it starts.
- **`dropped` parameters are never resent.** A provider that rejects `top_p` for a model rejects it on every call.
  Resending would fail every call the same way, and dropping it silently would make the parameter a lie.
- **The Ollama host guard is declared, not assumed.** A model load can push a small host (a laptop, a memory-capped
  virtual machine or distribution) into swapping or an out-of-memory kill. With the guard, a load first unloads
  every other model. No method ever restarts, stops or signals the host or what it runs in, which holds because
  the methods only ever call Ollama's HTTP API.
- **Review holds on doubt, and a person decides outside the run.** The options weighed:
  - Prompting only: safe, but an unattended run could never pass a threshold.
  - Holding only: works unattended, but makes a person at the terminal run the command and then run the
    whole run again.
  - Full pause and resume: needs the engine to persist and resume a run, which is Phase 27.

  So both of the first two are built, chosen by whether a person is present. Re-running reuses the decided
  answer, so it never bills twice, and the engine declares the pause it will later do instead of failing.
- **A rule that cannot be evaluated holds.** An unpriced model under `cost_over`, or a field missing from an
  answer, is exactly where an operator who set a threshold would want to look.
- **No platform files on the Walk tier**, covered next.

## The Walk tier

A Runner's per-task process has no project (`sdk.AIProject`) and no reviewer (`sdk.Reviewer`):
- A prompt is given inline. `prompt_file` is refused.
- A signed or `required` bundle is refused as uncheckable. An unsigned one under `optional` is recorded unsigned.
- `ai.route` is refused, because it reaches other providers through the project. Target the provider and use
  `ai.chat` instead.
- `usage_sink` and `save_as` are refused. Usage is in the job's results.
- An answer that matches a review rule **fails closed**: it is never released, and the error names Phase 27.

The Controller's prompt library, its trusted keys as settings, and approvals with an approver role are that
phase's.

## Reuse

| Built on | For |
|---|---|
| `pkg/httpapi` | The base URL rules |
| `pkg/devicetls` | The provider's TLS |
| The device record machinery and onboarding probers | The `ai_provider` type, as `generic_http`'s |
| `sdk.RecordWarnings`, `sdk.RecordInverse` | Warnings and undos |
| `internal/prompt` | The terminal |
| `internal/termsafe` | What a review prints |

New:

| Built | What it is |
|---|---|
| `pkg/aiprovider` | The kind registry, the four kinds, retries, streaming, usage and pricing |
| `pkg/aitask` | Parameters, prompts, review rules, routing tables, residency and the sink |
| `pkg/promptsign` | Canonical JSON, Ed25519, trusted keys and policy |
| `sdk.AIProject`, `sdk.Reviewer` | The project hook and the review hook |
| `engine.WithAIProject`, the review store, the pause declarations | The engine's side of those hooks |
| `ai_provider` | The device type |
| `internal/catalog/ai` | The methods |

## Failure modes

| Failure | Answer |
|---|---|
| A provider has no key and its kind needs one | Refused before contact, naming `add-credential`. |
| A use tagged local against a hosted provider | Refused at validate, and again before the first call, naming the use, the provider and both tags. |
| A parameter the kind does not accept | Reported `ignored`; the call goes on without it. |
| The provider rejects a parameter | Reported `dropped`; the call is sent once more without it, and never with it again in the run. |
| A retryable error past `retries` | Fails, naming the last status and how many tries. |
| A structured answer that misses the schema | Fails, naming the first violation. |
| A required signature missing, invalid, expired, from an unknown key or role, or rolled back | Refused before any call, naming which. |
| An answer matching a review rule | Held: decided at the terminal, or written to `ai/review` and the task fails `held`; on the Walk tier, fails closed. |
| A held request asked again | No call: the decision is reused, or the task fails as still pending or denied. |
| A model load on a host with no declared guard | Refused, naming the property. |
| The usage sink cannot be written | Refused before the call; a write failing after a call fails the task, saying the call was made. |
| Any failure | The error carries what a failure needs (the status, the dropped parameters, the verdict), bounded. An API key is never in it. |

---
status: beta
---

# ai.prompt.verify

Verifies a signed prompt bundle, parameter set or routing table against the project's trusted keys, as a call would.

Reads a document (inline as prompt with signature, or from the project as prompt_file with its .sig), refusing what two readers could read differently: a duplicate key at any depth, invalid UTF-8, an unpaired surrogate, a number with an exponent or beyond 2^53. It then checks, in order, that a trusted key (ai/trusted-keys.yaml) verifies the Ed25519 signature over its canonical form, that the key's role lists the document's use, that neither key nor document has expired, and that its version is not older than the newest of its name already used (.pleiades/prompt-versions/). Any failure fails the task, naming which. It records nothing, so a check of it is the task itself. A signature is provenance and integrity: it says who approved the text and that it is unchanged, not that what a model does with it is safe.

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
| `prompt` | `dict` | no | - | A prompt bundle given inline: {kind: prompt, name, version, use, text, variants, parameters, expires}, exactly the document its signature covers. One of messages, prompt and prompt_file. |
| `prompt_file` | `string` | no | - | A prompt bundle in the project by name: ai/prompts/<name>.json, with its signature, if any, in <name>.json.sig beside it. Crawl tier only. |
| `signature` | `dict` | no | - | An inline prompt's detached signature, {key, sig}, as pleiades ai prompt sign writes it. |
| `signing` | `string` | no | `required` | The use's signing policy. off checks nothing; optional checks a signature when there is one and records an unsigned bundle as unsigned, never as verified; required refuses an unsigned, invalid, expired, unknown-key, wrong-role or rolled-back bundle before any call. |
| `use` | `string` | no | - | The use the document must be for; any when absent. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `status` | `string` | success | verified; or, under a policy that admits them, unsigned or not checked. |
| `kind` | `string` | success | prompt, parameters or routes. |
| `name` | `string` | success | The document's name. |
| `version` | `int` | success | The document's version. |
| `use` | `string` | success | The use the document is for. |
| `hash` | `string` | success | The SHA-256 of its canonical form, in hex. |
| `key` | `string` | when signed | The signing key's id. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the document, its signature, the trusted keys and the version register, and records nothing: a version is recorded as used only by a call that sends it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `ai.chat`

## Examples

Refuse a release whose prompt is not signed:

```yaml
- name: Verify the triage prompt
  ai.prompt.verify:
    prompt_file: triage
    use: triage
```


# Language models as a step of a run, with the ai methods

The ai methods call a language model from a runbook: a local Ollama, a hosted provider, or anything that speaks
the OpenAI chat API. Each call is held to the use's data residency before it is made, metered in a usage record,
and, when its answer crosses a threshold you set, held for a person before any later task can read it.

## What is here

- `pleiades/inventory.yaml` names two model hosts you run (`workstation`, which declares the host guard that lets
  the runbook load a model there, and `lab-ollama`) and one hosted provider, `claude`. A provider's key is its
  own stored credential (`pleiades add-credential claude --token-stdin`), never in a file here.
- `pleiades/ai/prompts/triage.json` is a prompt bundle. Sign it with a key of yours to have runs verify it (below).
- `pleiades/ai/trusted-keys.yaml` lists the public keys trusted to sign prompts, and for which uses.
- `pleiades/runbooks/triage.yaml` does four things:
  - lists the workstation's models;
  - loads one model, unloading any other first;
  - triages a ticket with the bundle, giving the ticket as untrusted input;
  - summarizes through a routing table that falls back from the workstation to `lab-ollama`.

  The triage answer is held for a person when the model's confidence is under 0.8, or when it names a command
  that destroys data. Every call's usage is appended to `ai/usage/spend.jsonl`.

## Running it

```sh
cd pleiades
pleiades onboard workstation && pleiades onboard lab-ollama
pleiades validate runbooks/triage.yaml
pleiades run runbooks/triage.yaml --mode check   # calls no model, loads nothing, writes nothing: validates and estimates
pleiades run runbooks/triage.yaml
```

When the triage answer crosses a threshold, a person at the terminal is asked to approve, deny or hold it. With
nobody there (a schedule, a pipe, an agent), the task fails as `held`. Decide it at a terminal, then run again:
the decided answer is reused with no second call.

```sh
pleiades ai review list
pleiades ai review show <key>
pleiades ai review approve <key> --comment "checked against the ticket"
```

To sign the prompt, so a use can require it (`signing: required`):

```sh
pleiades ai prompt keygen --out ~/.config/pleiades/prompts.key --uses triage   # paste the entry into ai/trusted-keys.yaml
pleiades ai prompt sign ai/prompts/triage.json --key ~/.config/pleiades/prompts.key
```

What a signature means: it says who approved the prompt's text and that the text is unchanged. It is provenance
and integrity, not a promise that what the model does with the text is safe. The ticket is passed as `input`,
placed after the prompt between fixed markers as one JSON string, so it cannot close the prompt's own
instructions. The model still reads it.

A use tagged `local` never reaches a `hosted` provider. `pleiades validate` refuses a routing table that would do
that, and every call checks again before contact.

## What this example has been run against

`pleiades validate` accepts the runbook in the repository's own tests. `cmd/pleiades`'s ai release gate copies
this directory, points both model hosts at real HTTP servers speaking Ollama's API, onboards them, and runs the
runbook in check mode, checking that no model is called, nothing is loaded and nothing is written. The same gate
runs the methods for real: holding, approving, reusing and signing.

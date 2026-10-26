---
status: beta
---

# net.cli.command

Runs one show/exec-mode command against a network device's CLI.

Opens an interactive session over SSH (or over Telnet, with insecure_telnet) and runs one command, matched against the device's own declared cli_prompt property (a generic Dialect, built by netcli.FromPrompt, with no vendor-specific paging, configuration-mode or error convention of its own). Reports the command's own output, with its echoed input line and the trailing prompt both stripped. A command cannot be inspected, so this reports changed every time it reaches the device without error, the same convention exec.command established: pair it with when/when_or/when_cel when idempotence matters. Refuses outright when the target device's cli_prompt property is unset, rather than guessing at a prompt shape. Because this method has no known paging convention for a generic device, a command whose output is longer than the device's own terminal length can pause on a pager prompt this method cannot answer (verified directly against a real device); each command is bounded to 30 seconds so that failure is a clear, timely error rather than an indefinite hang. Pipe a long-output command through the device's own output filter (e.g. "show running-config | include hostname") to avoid triggering it, or use net.ios.config, whose vendor-specific dialect disables paging for real. A command that asks questions before it finishes is answered from prompts, each answer written in the task, and a question nobody listed can go to a model with ask, which picks one of the task's own choices by id or refuses; expect_disconnect counts the device closing the session after an answer as success. The 30 seconds cover the whole command, its questions included.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetworkCLICapable` |
| Transports | `ssh`, `telnet`, `console` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since an arbitrary CLI line can be anything the device accepts, and what it changes, if anything, is known only once the device has run it |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `command` | `string` | yes | - | The single CLI line to run, e.g. "show version". |
| `prompts` | `list of dict` | no | - | **Accepts `{{ secret.<name>.<field> }}`:** Questions the command may ask, each {match, answer} or {match, refuse: true}. match is RE2 found on the line the device waits on (case-sensitive unless it starts with (?i)); answer is typed and then Enter, "" being Enter alone; refuse closes the session, which abandons the command, and fails the task. The first unanswered entry matching is used and each answers once, so a question the device asks again after its answer fails the task; list it twice to answer it twice. Text written in the task is never sent to a question asking for a password, passphrase or secret; answer one with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret), which is resolved just before it is typed, may stand only with literal text, and is recorded in exchanges as (secret). |
| `ask` | `dict` | no | - | A model for questions no prompts entry matches, on the Crawl tier: choices, a list of {id, answer, means} the model picks from by id (it never types its own answer, and may refuse, which fails the task, or say the line was no question); max, how many it may answer (1 to 10, default 3); shape, the RE2 a waiting line must match to be asked about (default: ending in ?, ] or ]:); and ai.chat's own provider, model, residency, prompt or prompt_file (a built-in prompt when neither), signing, review, usage_sink and call options. Its provider is resolved before the device is reached. A held answer fails the task as held, which abandons the command; decided with pleiades ai review, running again reuses the decision for that same question. |
| `expect_disconnect` | `bool` | no | `false` | Count the device closing the session after at least one answer as success, which is how a reload ends. A close before any answer still fails. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `stdout` | `string` | always | Everything the device printed in response to the command, its questions and the echo of each answer included, with the echoed command line and the trailing prompt removed. |
| `exchanges` | `list of dict` | when prompts, ask or expect_disconnect is set | Each question the device asked and what was typed: question and answer, with prompt (the entry's index) or, for the model, choice, reason, provider, model, confidence, usage and review. |
| `disconnected` | `bool` | when prompts, ask or expect_disconnect is set | Whether the device closed the session after an answer (expect_disconnect). |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

An arbitrary CLI line's effect on a device is unknown to this platform, so no undo can be derived from it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.cli.config`
- `net.ios.command`
- `net.ios.config`

## Examples

Read a device's software version:

```yaml
- name: Check the running version
  net.cli.command:
    command: show version
  register: version
```

Answer the question a command asks:

```yaml
- name: Copy the configuration to a file
  net.cli.command:
    command: copy running-config flash:before-change.cfg
    prompts:
      - match: 'Destination filename \[[^\]]*\]\?'
        answer: ""
      - match: 'over ?write\? \[confirm\]'
        refuse: true
```


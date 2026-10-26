---
status: beta
---

# net.ios.command

Runs one exec-mode command on a Cisco IOS device, answering the questions it asks as the task says.

Opens an interactive PTY session over SSH using netcli.IOS's own paging, prompt and error conventions, so it needs no cli_prompt property and long output is never paused by a pager, and runs one command. A line in IOS's error forms ("% ..." or "%Error ...") fails the task, unless accept takes it; a command that only reads fails only on IOS's own refusal of the line (% Invalid input, % Incomplete command, % Ambiguous command, % Unrecognized command, %Error), since a "% " line in what it shows is the device's data. A command that asks questions before it finishes (copy asks for a destination filename, delete and reload ask to confirm) is answered from prompts, each answer written in the task. A question nobody listed can go to a model with ask, which picks one of the task's own choices by id or refuses, and never types an answer of its own: on IOS one Enter both accepts a default filename and confirms a reload, so only the task's author knows what an answer does. A question nobody answers waits until the task's timeout, and the failure names it. With expect_disconnect, the device closing the session after an answer is the command's success, which is how reload ends. It has no time limit of its own, so set the task's timeout for a long copy. The output is everything the device printed after the echoed command, its questions and the echo of each answer included, with the trailing prompt removed and its secrets (a pre-shared key, a password, a community) masked; every named secret of the project is masked by value besides. A command that only reads (show, or IOS's abbreviation of it, and dir, more, ping, traceroute and terminal written in full, the last changing only this session) reports unchanged, unless its output is sent to a file with | redirect, | append or | tee; any other command, debug among them, cannot be inspected, so it reports changed every time it runs without error.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh`, `telnet`, `console` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since an arbitrary command can be anything the device accepts, and what it changes, if anything, is known only once the device has run it |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `command` | `string` | yes | - | The single exec-mode line to run, e.g. "show version" or "copy http://192.0.2.5/x.bin flash:x.bin". |
| `accept` | `list of string` | no | - | Further IOS "% " lines to take as output rather than a refusal, each RE2 matched from the start of the line and so beginning with %, as net.ios.config's accept. A command that only reads needs none: it fails only on IOS's own refusal of the line. |
| `prompts` | `list of dict` | no | - | Questions the command may ask, each {match, answer} or {match, refuse: true}. match is RE2 found on the line the device waits on (case-sensitive unless it starts with (?i)); answer is typed and then Enter, "" being Enter alone; refuse closes the session, which abandons the command, and fails the task. The first unanswered entry matching is used and each answers once, so a question the device asks again after its answer fails the task; list it twice to answer it twice. Text written in the task is never sent to a question asking for a password, passphrase or secret; answer one with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret), which is resolved just before it is typed, may stand only with literal text, and is recorded in exchanges as (secret). |
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

An arbitrary IOS command's effect (a copy, a delete, a reload) is unknown to this platform, so no undo can be derived from it. Give the task a rollback: list naming what undoes it when one exists.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.cli.command`
- `net.ios.config`
- `net.ios.save`
- `ai.chat`

## Examples

Copy an image to flash, refusing to overwrite one already there:

```yaml
- name: Pull the new image onto flash
  net.ios.command:
    command: copy http://192.0.2.5/images/c8000v-17.15.04c.SPA.bin flash:c8000v-17.15.04c.SPA.bin
    prompts:
      - match: 'Destination filename \[[^\]]*\]\?'
        answer: ""
      - match: 'over ?write\? \[confirm\]'
        refuse: true
  timeout: 1800
```

Reload, ending when the device drops the session:

```yaml
- name: Reload the router
  net.ios.command:
    command: reload
    prompts:
      - match: 'Proceed with reload\? \[confirm\]'
        answer: ""
      - match: 'Save\? \[yes/no\]'
        refuse: true
    expect_disconnect: true
```

Let a local model answer a question nobody listed:

```yaml
- name: Delete the old image
  net.ios.command:
    command: delete flash:c8000v-17.12.01.SPA.bin
    prompts:
      - match: 'Delete filename \[[^\]]*\]\?'
        answer: ""
    ask:
      provider: workstation
      model: gemma4:e4b-it-qat
      residency: local
      choices:
        - id: confirm
          answer: ""
          means: Press Enter to confirm deleting the file the question names.
        - id: decline
          answer: "n"
          means: Answer no and keep the file.
      review:
        - field: confidence
          below: 0.8
```


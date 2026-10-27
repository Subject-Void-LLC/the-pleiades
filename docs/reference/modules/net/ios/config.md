---
status: beta
---

# net.ios.config

Applies configuration lines to a Cisco IOS device, keeping a checkpoint of the configuration before them so the change can be undone exactly.

Opens an interactive session over SSH (or over Telnet, with insecure_telnet), using netcli.IOS's own paging, configuration-mode and error conventions, and applies lines as a batch: "configure terminal", each line in order, then "end". Aborts on the first line the device rejects (an IOS "% ..." error), still leaving configuration mode before returning that error. A "% " line IOS prints for a line it accepted is a notice, not a rejection, and is reported under notices: an access list entry, IKEv2 proposal or profile match already there, a key pair being made or replaced, and what removing one says first; accept adds the task's own. A line that changes key material (crypto key generate, zeroize, import, move, decrypt, encrypt) changes what the configuration does not hold, so its run reports changed whatever the configuration shows, and no checkpoint returns it: such a call's undo is never exact. A line that asks a question (removing a key pair asks to confirm) is answered from prompts, each answer written in the task, or by a model with ask, which picks one of the task's own choices by id or refuses; a question nothing answers (a line left ending in ?, [confirm], [yes/no] or asking for a password) fails the line at once, naming it, rather than waiting; anything else a device waits on, such as a banner's text, still waits until the read limit. Before the lines, it copies the running configuration to a checkpoint file on the device (its default writable disk or flash, such as unix: or flash:), and after them the device's own show archive config differences says what changed: no difference reports unchanged and deletes the checkpoint, so a second run of the same lines changes nothing. A change copies the configuration it left to a second checkpoint and records its undo as net.ios.replace naming both files, which returns the device exactly to the first (configure replace) and refuses if the device no longer matches the second. The files stay on the device until that undo runs. A device with no writable file system, or no configure replace, records no undo, with a warning: the configuration lines that would undo the change, written from its configuration read before and after it (the negation of each added line, each removed line put back), are reported under undo_lines, masked, for the task's rollback: list, since lines read off a device never reach the journal. A change inside an access list, a banner or a certificate has no such lines. checkpoint: false does the same on any device. The differences this reports are masked as net.batfish.check masks a candidate.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh`, `telnet`, `console` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since what a configuration line changes is decided by IOS's own parser as it applies the line, and IOS offers no way to ask without applying it |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lines` | `list of string` | yes | - | **Accepts `{{ secret.<name>.<field> }}`:** The configuration lines to apply, in order, WITHOUT "configure terminal" or "end": this method supplies both itself. A line may name a stored secret, such as a pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the line is sent, and masked in everything the run reports. A line holding one holds no other template expression, and a value with whitespace, a control character or ? is refused. |
| `checkpoint` | `bool` | no | `true` | Keep a checkpoint of the running configuration on the device before the change, which its undo returns to exactly. false records no undo, and reports the lines that would undo the change for the task's rollback: list. |
| `backup` | `bool` | no | `false` | Capture the device's running-config with "show running-config" before applying any line, recorded under the backup stat. |
| `accept` | `list of string` | no | - | Further IOS "% " lines to take as notices rather than rejections, each RE2 matched from the start of the line and so beginning with %, e.g. "% Frob is deprecated". Every other "% " line still fails the task. |
| `prompts` | `list of dict` | no | - | **Accepts `{{ secret.<name>.<field> }}`:** Questions the command may ask, each {match, answer} or {match, refuse: true}. match is RE2 found on the line the device waits on (case-sensitive unless it starts with (?i)); answer is typed and then Enter, "" being Enter alone; refuse closes the session, which abandons the command, and fails the task. The first unanswered entry matching is used and each answers once, so a question the device asks again after its answer fails the task; list it twice to answer it twice. Text written in the task is never sent to a question asking for a password, passphrase or secret; answer one with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret), which is resolved just before it is typed, may stand only with literal text, and is recorded in exchanges as (secret). |
| `ask` | `dict` | no | - | A model for questions no prompts entry matches, on the Crawl tier: choices, a list of {id, answer, means} the model picks from by id (it never types its own answer, and may refuse, which fails the task, or say the line was no question); max, how many it may answer (1 to 10, default 3); shape, the RE2 a waiting line must match to be asked about (default: ending in ?, ] or ]:); and ai.chat's own provider, model, residency, prompt or prompt_file (a built-in prompt when neither), signing, review, usage_sink and call options. Its provider is resolved before the device is reached. A held answer fails the task as held, which abandons the command; decided with pleiades ai review, running again reuses the decision for that same question. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |
| `insecure_telnet` | `bool` | no | `false` | Reach a device that has no SSH over bare Telnet, answering its login with the task's credential. Telnet carries everything, the login included, in cleartext, so set this only for a device on a network you trust. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `checkpoint` | `string` | when a checkpoint was kept and the lines changed the configuration | The checkpoint file on the device holding the configuration before the change, which the undo returns to. |
| `differences` | `list of string` | when a checkpoint was kept | The device's own contextual differences the change made (+ added, - removed), masked. |
| `undo_lines` | `list of string` | when no checkpoint was kept and the lines changed the configuration | The configuration lines that would undo the change, masked; copy them into the task's rollback: list, putting each masked secret back. |
| `backup` | `string` | when backup is true | The device's full running-config, captured immediately before this task's own lines were applied. Not sanitized: it genuinely contains this device's own enable secret, enable password, local user password hashes, and any TACACS+/RADIUS shared key in whatever strength (or weakness -- IOS's own "type 7" is trivially reversible) encoding the device applies. Mask it with "register_mask: backup" on this task. |
| `notices` | `list of string` | when the device printed one | The "% " lines the device printed that were taken as notices rather than rejections, masked. |
| `exchanges` | `list of dict` | when prompts or ask is set | Each question a line asked and what was typed: question and answer, with prompt (the entry's index) or, for the model, choice, reason, provider, model, confidence, usage and review. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo returns the device to a checkpoint of its running configuration kept on the device (net.ios.replace, named by the file's path alone, so no configuration text reaches the journal), refusing when the device has changed since. A device with nowhere to keep one records no undo: the configuration lines that would undo the change are shown, masked, for the task's rollback: list, since lines read off the device never reach the journal.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ios.replace`
- `net.ios.parse`
- `net.cli.command`
- `net.cli.config`

## Examples

Create a loopback interface, undoable by pleiades rollback:

```yaml
- name: Add a loopback interface
  net.ios.config:
    lines:
      - interface Loopback0
      - description managed by pleiades
```

Remove a key pair, answering the question IOS asks first:

```yaml
- name: Remove the old key pair, which no rollback brings back
  net.ios.config:
    lines:
      - crypto key zeroize rsa OLDKEY
    prompts:
      - match: 'Do you really want to remove these keys\? \[yes/no\]'
        answer: "yes"
    accept:
      - '% The specified keypair does not exist'
```


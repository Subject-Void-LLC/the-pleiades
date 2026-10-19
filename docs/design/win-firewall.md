# Design note: `win.firewall.rule` and `win.firewall.profile`

Status: built. Two methods for Windows Defender Firewall, the Windows counterpart of `fw.firewalld.*`: its rules, and its
three profiles. It was
found missing when a domain controller's sshd was running and unreachable: Windows Server 2025's own
`OpenSSH-Server-In-TCP` rule covers the Private profile only, and promotion makes the controller's adapters
DomainAuthenticated.

## The rule and its store

- **Identity.** A rule is named by its `Name` (`OpenSSH-Server-In-TCP`, or a GUID in braces), never its display
  name, which is not unique.
- **Store.** Every read and write is of the machine's own `PersistentStore`. A rule that exists only in the active
  store (Group Policy) is refused, since a change made here would be overwritten.
- **State.** `present` makes or changes it, `absent` removes it, `query` only reports it.

## What a call manages

- **A new rule** takes Windows' defaults for what the task leaves out: inbound, allow, any protocol, every profile,
  enabled, and its name as its display name.
- **An existing rule** is compared and changed only on the settings the task gives. Widening a built-in rule's
  profiles does not restate, or reset, its ports, program or description.
- **Comparison.** Profiles, ports and addresses compare as sets. An IPv4 network compares the same written as a
  prefix (`10.20.0.0/16`) or with the dotted mask Windows answers with. Port keywords (RPC, RPCEPMap, IPHTTPS,
  Teredo) and address keywords (LocalSubnet, Internet, DNS and the rest) are accepted.
- **Ports need tcp or udp:** the task's protocol, or the rule's when the task names none. A mismatch is refused
  before anything is sent.

## How it runs

The script is a constant, sent through `pkg/winrmexec`; every value reaches it as ASCII JSON on standard input, held
to its form first. After a change, the rule is read back and compared again; a host that answers without applying
the change fails the task.

## Undo and check

| Run | Undo |
|---|---|
| Made a rule | Remove it. |
| Changed settings | Put back the earlier value of each one changed. |
| Removed a rule | Make it again with every setting it had, outside the group it was in (a rule cannot be put back into one). |

Every undo value is a single value the run journal keeps: lists are written as one comma-separated string, which every
list parameter also accepts. The display name and description are text the device holds, withheld from the journal,
so an undo restoring either replays only from the task's own `rollback:` list. A check reads the rule and reports the
differences; it sends nothing.

## What is proved, and how

- The parsing, comparison, undo and refusals run against a stand-in that answers in the forms Windows writes.
- On the lab's domain controller, through `pleiades`: a query, a check, widening `OpenSSH-Server-In-TCP` (port 22
  answering from outside), `pleiades rollback` closing it again, and a new rule made, run again with no change, and
  rolled back to nothing.

## `win.firewall.profile`

- **One profile per task**, `domain`, `private` or `public`, so the undo, one call, can put back exactly what the
  task changed. A task giving no setting only reports the profile.
- **Settings:** `enabled`, `default_inbound_action` and `default_outbound_action` (`allow` or `block`),
  `log_allowed`, `log_blocked`, `log_file` and `log_max_size_kb`. Only those a task gives are compared and changed.
- **NotConfigured** compares as what it does: on, block inbound, allow outbound, log nothing. An undo restoring one
  writes that value explicitly, which behaves the same.
- **Group Policy.** The settings in force (`ActiveStore`) are read beside the machine's own. A setting a policy holds
  at another value fails the task, naming it, before anything is sent when the machine's own setting already matches,
  and after the change when one was made (its undo is recorded).
- **Proved** on the lab's domain controller: a report, a check naming exactly the two settings asked, the change, a
  second run with no change, and `pleiades rollback` restoring the stock values.

## The scripts' function names

Each script defines one helper function, and each is named verb-noun (`Read-Rule`, `Read-Profile`). PowerShell runs
an alias before a function of the same name, and short names are aliases: the profile script's first helper, `R`, ran
`Invoke-History` on the domain controller. A test refuses a helper name that is not verb-noun.

---
status: beta
---

# net.frr.config

Makes an FRRouting router run exactly the configuration given, and saves what it ran before so the change can be undone.

Takes the whole intended configuration (config, or src: a file in the project) and applies only what differs, through FRR's own frr-reload.py. The candidate is first checked with vtysh -m -f, and a line vtysh refuses fails the task before anything reaches the router: frr-reload reads a file vtysh cannot parse as an empty configuration and would delete everything. The difference comes from frr-reload.py --test; an empty one is no change, and a check returns it as the prediction. A real run saves the running configuration to a file on the router (0600, under the connecting account's ~/.pleiades-frr), reloads, confirms the running configuration now matches the candidate, and saves the result beside the backup. Its undo names only that backup, so no configuration text, and no secret in one, reaches the journal; the undo restores exactly the configuration that was running, and refuses when the router no longer runs what this change left, unless its step names force. The undo is recorded before the reload and kept if the reload then fails, since restoring a backup when nothing changed is itself no change. With save, the running configuration is also written to frr.conf, keeping that file's owner and mode, so it survives a restart; the backup then keeps frr.conf as it was too, and the undo puts it back (where there was none, the undo writes the configuration it restores). expect_sha256 refuses a candidate whose bytes are not the ones a model check analysed.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FRRCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `config` | `string` | no | - | The whole intended configuration, as frr.conf text. Set config or src, not both. Lines not in it are removed from the running configuration. |
| `src` | `string` | no | - | A file in the project directory holding the whole intended configuration, read by the command line; refused on a Walk-tier Runner, where config is the way. Set config or src, not both. |
| `save` | `bool` | no | `false` | Also write the configuration to frr.conf (keeping that file's owner and mode), so it survives a restart. The undo puts frr.conf back as it was. |
| `expect_sha256` | `string` | no | - | Refuse unless the candidate's SHA-256 is this, in hex: the digest net.batfish.check returned for the bytes it analysed, so the change applied is the change checked. |
| `restore` | `string` | no | - | Restore the configuration this method saved before an earlier change, by the path its undo recorded. Set by an undo; with restore, config and src are not given. |
| `force` | `bool` | no | `false` | With restore, restore even though the router no longer runs what the earlier change left (someone changed it since). |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `lines_added` | `list of string` | always | The configuration lines frr-reload added, as it reported them. |
| `lines_deleted` | `list of string` | always | The configuration lines frr-reload removed. |
| `sha256` | `string` | always | The SHA-256 of the configuration applied, in hex. |
| `backup` | `string` | when something changed | The path of the running configuration saved before the change, on the router. |
| `inverse` | `dict` | when something changed | The undo: this method with restore set to backup (and save when the change saved), held whole in the journal. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo restores the running configuration saved on the router before the change (and frr.conf, when the change saved), named by the backup's path alone, and refuses when the router has been changed since unless forced.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.frr.facts`
- `net.frr.interface`
- `net.batfish.check`
- `wait.routes`

## Examples

Apply a router's intended configuration:

```yaml
- name: r1 runs its intended configuration
  net.frr.config:
    target: r1
    src: configs/r1.frr.conf
    save: true
```


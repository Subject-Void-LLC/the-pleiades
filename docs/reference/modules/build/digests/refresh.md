---
status: beta
---

# build.digests.refresh

Pins every container image reference in a build's files to the digest its tag names now.

Reads the named files on the device, resolves each reference's tag to the digest it names now through the device's container engine (for a multi-platform image the index's digest, which pins every platform, never one platform's), and rewrites each reference that changes as name:tag@sha256:..., keeping its own spelling of the name. Every reference is resolved before anything is written, so a registry refusing one (gone, rate limited, unauthorized) leaves every file as it was. A reference with no tag, or one built from a build argument with no default, is refused, since nothing says what to refresh it to. Each file is rewritten once, only if it still holds what was read, and keeps its mode, owner and group: the new content is written beside it and renamed over it, or, when a new file cannot be given its owner and group by this login, copied into it in place (named in warnings); a write that fails puts back every file it had rewritten. Each change is reported as file, line, from and to. With set, the given rewrites are made instead of resolving any: each names a file, a line, the reference there now and the one to write. Before writing anything, a run keeps the references it replaces in an undo record on the device, in the login's own state directory (${XDG_STATE_HOME:-~/.local/state}/pleiades/build-digests, private to it, records older than 90 days removed by the next run), and its recorded undo is this method with undo naming that record, which writes each reference back only where the file still holds what the run wrote. A check resolves every reference as a run does and reports the changes it would make, writing nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. **Choices:** `docker`, `podman`. |
| `files` | `list of string` | no | - | Required unless undo is given. The files on the device to read, each recognized by its name and content: a Dockerfile or Containerfile (each FROM, COPY --from and RUN --mount from that names an image, a FROM through an ARG read from the ARG's default), a Compose file (each service's image), or Kubernetes manifests (every container's image, in every document). A file of none of these kinds is refused by name. Each is read whole, at most 1 MiB. |
| `set` | `list of dict` | no | - | Exact rewrites to make instead of resolving: each a mapping of file, line, from (the reference written there now) and to (the reference to write, which must parse). A file must also be in files. An entry whose from is not on that line refuses the task. |
| `undo` | `string` | no | - | The id of an undo record an earlier run of this method kept on the device: the references it names are written back as they were, and files, when not given, are the files it names. Cannot be combined with set. A run that writes keeps a record of its own, and the record it replayed is removed. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `changes` | `list of dict` | always | Each reference changed, or that a check would change: file, line, from and to. |
| `warnings` | `list of string` | when a file was written in place | Each file written in place rather than renamed over, and why. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo writes back each reference it changed, as it was, from a record it keeps on the device before writing anything (the journal holds only the record's id); a file edited since is refused rather than overwritten.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `build.digests.check`

## Examples

Move a project's image pins forward:

```yaml
- name: Refresh image pins
  build.digests.refresh:
    files: [/srv/app/Dockerfile, /srv/app/compose.yaml]
```


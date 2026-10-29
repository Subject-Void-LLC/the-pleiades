---
status: beta
---

# build.digests.check

Lists every container image reference in a build's files that is not pinned by both tag and digest.

Reads the named files on the device and checks each image reference in them: pinned means named by both tag and digest (name:tag@sha256:...), since a tag alone moves underneath a build and a digest alone says nothing about what it is. A reference built from a build argument with no default, or from a variable a Compose file is interpolated with, cannot be read as written and is an offender too. With resolve, each pinned reference's tag is also resolved to the digest it names now, through the device's container engine (its own network, proxy, mirrors and registry logins, and the run's credential for that registry when it has one), and a pinned digest the tag no longer names is reported as drift. Any offender fails the task, its error naming them, and the task's returns are reported with the failure. Nothing is written.

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
| `files` | `list of string` | yes | - | The files on the device to read, each recognized by its name and content: a Dockerfile or Containerfile (each FROM, COPY --from and RUN --mount from that names an image, a FROM through an ARG read from the ARG's default), a Compose file (each service's image), or Kubernetes manifests (every container's image, in every document). A file of none of these kinds is refused by name. Each is read whole, at most 1 MiB. |
| `resolve` | `bool` | no | `false` | Also resolve each pinned reference's tag through the device's container engine and report drift. The device must have its engine onboarded. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `references` | `list of dict` | when the files were read | Every reference found: file, line, reference, where (FROM, COPY --from, RUN --mount, ARG NAME, image), pinned, problem (empty when there is none), and current (the digest its tag names now, when it drifted). |
| `offenders` | `int` | when the files were read | How many references are offenders, which is zero for a task that succeeded. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads files and, with resolve, asks the registry; it writes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `build.digests.refresh`
- `build.lock.verify`

## Examples

Check a project's images are pinned:

```yaml
- name: Check image pins
  build.digests.check:
    files: [/srv/app/Dockerfile, /srv/app/compose.yaml, /srv/app/k8s/deploy.yaml]
```


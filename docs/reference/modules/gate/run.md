---
status: beta
---

# gate.run

Runs a project's gate manifest of named checks on the device and reports PASS, FAIL or SKIP for each.

Reads a gate manifest (YAML, at most 1 MiB, decoded strictly) from the device and runs the checks the task selects, one result per check. A check runs its command in a directory on the device over SSH, or in a fresh container from a digest-pinned image on the device's container engine, with no network unless it names one, a read-only root with a writable /tmp unless it says writable, and no new privileges; the image is pulled only when the check says pull. Every check has a timeout, its own or the manifest's default, and there is no implicit one; a directory check's is enforced on the device with the timeout command, which the device must have, and an image check's by killing its container. Checks run one at a time unless the manifest's defaults.parallelism or the task's parallelism raises it, at most one heavy check runs at a time, and a serial check runs with nothing else. A check skips, rather than runs to fail, when a precondition it declares is not met (a command on the PATH, an environment variable set, a file, the operating system, the container engine, a TCP endpoint the device reaches), or when it exits with a code its skip_exit lists; a check the task did not select, or that the task's deadline or a cancellation reached before it started, is skipped too, saying why. No exit code means skipped unless a check declares it, and a SKIP is never counted as a PASS. Any FAIL fails the task, and with fail_on_skip so does any SKIP. A failed task's error carries each failing check's reason and the end of its output, since a failed task's returns are not kept. The output kept is the last 40 lines and 8 KiB of a failing or skipping check, with anything a terminal would act on escaped. The manifest is refused whole, before anything runs, for a check with no kind, a kind neither built in (pure, db, network, model) nor declared, a check with no timeout, an image not pinned by digest, and every other rule it breaks. A check gets no environment and no credential from the platform. A check is not checkable: whether it passes is known only by running it, so a check of this task reads and validates the manifest and the selection, then reports the task unchecked.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported for some calls, named in the description: those report what they would change and change nothing, a check run names any other as unchecked, and validation refuses check_mode on one |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. |
| `manifest` | `string` | yes | - | The gate manifest's path on the device. A check's dir is relative to the manifest's own directory. |
| `kinds` | `list of string` | no | - | Run only checks of these kinds. Every kind when absent. A kind the manifest does not have is refused. |
| `checks` | `list of string` | no | - | Run only these checks, by name, among those the kinds select. A name the manifest does not have is refused. |
| `heavy` | `string` | no | `include` | include runs heavy checks with the rest, exclude leaves them out, only runs nothing else. A selection that runs no check at all is refused, since it would read as a pass. |
| `parallelism` | `int` | no | - | How many checks run at once, overriding the manifest's defaults.parallelism; 1 when neither sets it, at most 16. Heavy checks still run one at a time and serial checks alone. |
| `fail_on_skip` | `bool` | no | `false` | Fail the task when any check is skipped, for a gate whose every check must run. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `checks` | `list of dict` | on success | One entry per check in the manifest, in its order: name, kind, status (PASS, FAIL or SKIP), reason, duration_ms, exit_code (-1 when it did not run to an exit status) and tail (the end of a failing or skipping check's output). |
| `passed` | `int` | on success | How many checks passed. |
| `failed` | `int` | on success | How many checks failed, which is zero for a task that succeeded. |
| `skipped` | `int` | on success | How many checks were skipped, for any reason. |
| `summary` | `string` | on success | The counts for a person, for example "12 passed, 0 failed, 3 skipped". |
| `ledger` | `string` | when a check was skipped | The skipped checks grouped by reason, largest group first. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It runs the project's own checks and changes nothing of its own; what a check writes is that check's doing, which this platform cannot see to undo. It is not declared read-only, since a check is a command.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `gate.list`
- `exec.command`

## Examples

Run a project's pure and db checks:

```yaml
- name: Gate the release
  gate.run:
    manifest: /srv/app/gates.yaml
    kinds: [pure, db]
```

Run only the heavy checks, two at a time:

```yaml
- name: Run the slow suites
  gate.run:
    manifest: /srv/app/gates.yaml
    heavy: only
    parallelism: 2
```


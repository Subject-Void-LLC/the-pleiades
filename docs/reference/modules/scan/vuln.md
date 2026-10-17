---
status: beta
---

# scan.vuln

Lists the known vulnerabilities in an image archive, a directory or lockfiles, and fails at a severity.

Runs OSV-Scanner over an image archive (docker save), a directory (recursively, every lockfile and manifest it recognizes) or named lockfiles on the device, and reports each package exposed to each group of aliased advisories: its severity from the highest CVSS score (critical from 9.0, high from 7.0, medium from 4.0, low below), and the versions that fix it. A vulnerability at or above fail_on fails the task, and with fixable_only only one a fixed version exists for; one with no score is gated as high, since an advisory nobody has scored is not evidence it is harmless. Online, the scanner joins the engine's default network and sends osv.dev the names and versions of the packages it found; with offline_db it joins none and reads OSV-Scanner's local databases from the device. A directory holding an osv-scanner.toml at any depth, which OSV-Scanner would read as a list of vulnerabilities to ignore with no reason or end, refuses the scan, and none is read in any mode; a directory the search cannot read whole is refused too. It only reads, so a check is the scan a run makes and sends what a run sends (give offline_db for one that sends nothing), except that a check never pulls the scanner's image: without it, the check says it cannot check. The scanner runs in a fresh container on the device's own engine, from an image pinned here by its multi-platform index digest, so nothing is installed on the device and every run uses the scanner this release was tested with; what it scans is mounted read-only, its root is read-only, it runs with no-new-privileges and is removed afterwards. Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CommandExecCapable`, `ContainerEngineCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `dir` | `string` | no | - | A directory on the device to scan, recursively. One of dir, image_archive and lockfiles. |
| `image_archive` | `string` | no | - | An image archive on the device, as docker save or podman save writes one. |
| `lockfiles` | `list of string` | no | - | Lockfiles on the device to scan, each recognized by its name (package-lock.json, Cargo.lock, go.mod, requirements.txt and the rest OSV-Scanner reads). |
| `fail_on` | `string` | no | `high` | The least severity that fails the task: critical, high, medium, low, or any (every finding, scored or not). |
| `fixable_only` | `bool` | no | `false` | Fail only on vulnerabilities a fixed version exists for; the rest are still reported. |
| `allow_empty` | `bool` | no | `false` | Pass, with a warning, when OSV-Scanner finds no package to check. Without it that fails the task, since a scan that saw nothing (a wrong path, or files the scanner could not read) is no evidence of no vulnerability. |
| `offline_db` | `string` | no | - | A directory on the device holding OSV-Scanner's local databases (osv-scalibr/<ecosystem>/all.zip, as osv-scanner --download-offline-databases writes them); the scan then joins no network, and refuses an ecosystem it has no database for. |
| `ignore` | `string` | no | - | An ignore file on the device: a YAML list of {id, reason, until}, every field required, until a date (YYYY-MM-DD, in UTC) no more than 366 days after the day of the run, since an end years away is no end. An entry suppresses its finding through its until day; past it, it suppresses nothing and the run warns, and an entry that matched nothing is warned about so the file shrinks as fixes land. |
| `timeout` | `string` | no | `15m` | How long the scanner may run, up to 2h, after which its container is killed and removed. |
| `pull` | `bool` | no | `true` | Pull the scanner's pinned image when the engine does not have it, in a run: a check never pulls, and says it cannot check without the image. The image is pinned by digest, so a pull cannot substitute another; a device that cannot pull needs it loaded first. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `findings` | `list of dict` | on success | Up to 1000 findings: package, version, ecosystem, id, aliases, severity, score, fixed, source (the file on the device it was found in), and ignored (the reason) when an ignore entry suppressed it. |
| `failing` | `int` | on success | How many findings fail the gate, which is zero for a task that succeeded. |
| `counts` | `dict` | on success | How many findings at each severity. |
| `scanner` | `string` | always | The scanner's image, pinned by digest. |
| `warnings` | `list of string` | when there is one | Ignore entries expired or unused, and anything else the run must say. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads what it scans, mounted read-only into the scanner's container, and writes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `scan.sbom`
- `build.lock.verify`

## Examples

Gate a release on fixable high vulnerabilities:

```yaml
- name: Known vulnerabilities
  scan.vuln:
    image_archive: /srv/release/app.tar
    fail_on: high
    fixable_only: true
    ignore: /srv/app/scan-ignore.yaml
```


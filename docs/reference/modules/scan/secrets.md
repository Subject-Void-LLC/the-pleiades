---
status: beta
---

# scan.secrets

Finds credentials leaked into a directory, naming each by file, line and rule, never by its value.

Runs Gitleaks over a directory on the device with its default rules and no network, and fails on every finding an ignore entry does not suppress, each named by its file (relative to dir), line, rule and fingerprint (file:rule:line, the id an ignore entry names). Gitleaks runs with --redact, so no value is in its report, and this reads only a finding's file, line and rule, so no value could be kept even if one were. A finding means rotating the credential, not only removing it. A file or directory Gitleaks could not read, which it skips with only a warning, fails the scan, named: what was not read was not scanned. A .gitleaks.toml or .gitleaksignore at the directory's root, which Gitleaks would read as its own rules or ignores, refuses the scan, and an inline gitleaks:allow comment suppresses nothing: what is scanned never decides what is reported. It only reads, so a check is the scan a run makes, except that a check never pulls the scanner's image: without it, the check says it cannot check. The scanner runs in a fresh container on the device's own engine, from an image pinned here by its multi-platform index digest, so nothing is installed on the device and every run uses the scanner this release was tested with; what it scans is mounted read-only, its root is read-only, it runs with no-new-privileges and is removed afterwards. Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket.

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
| `dir` | `string` | yes | - | The directory on the device to scan. |
| `ignore` | `string` | no | - | An ignore file on the device: a YAML list of {id, reason, until}, every field required, until a date (YYYY-MM-DD, in UTC) no more than 366 days after the day of the run, since an end years away is no end. An entry suppresses its finding through its until day; past it, it suppresses nothing and the run warns, and an entry that matched nothing is warned about so the file shrinks as fixes land. |
| `timeout` | `string` | no | `15m` | How long the scanner may run, up to 2h, after which its container is killed and removed. |
| `pull` | `bool` | no | `true` | Pull the scanner's pinned image when the engine does not have it, in a run: a check never pulls, and says it cannot check without the image. The image is pinned by digest, so a pull cannot substitute another; a device that cannot pull needs the image on its engine under that digest first. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `findings` | `list of dict` | on success | Up to 1000 findings: file, line, rule, fingerprint, and ignored (the reason) when an ignore entry suppressed it. |
| `failing` | `int` | on success | How many findings no ignore entry suppressed, which is zero for a task that succeeded. |
| `scanner` | `string` | always | The scanner's image, pinned by digest. |
| `warnings` | `list of string` | when there is one | Ignore entries expired or unused, and anything else the run must say. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the tree, mounted read-only into the scanner's container, and writes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `scan.vuln`

## Examples

No credential in the release tree:

```yaml
- name: Leaked credentials
  scan.secrets:
    dir: /srv/release/app
    ignore: /srv/app/scan-ignore.yaml
```


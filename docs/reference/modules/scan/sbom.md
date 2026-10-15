---
status: beta
---

# scan.sbom

Writes an SPDX 2.3 SBOM of an image archive or a directory, held to the NTIA minimum elements, with its SHA256SUMS.

Runs Syft over an image archive or a directory on the device with no network, names the SBOM's subject with the task's name and version, records the task's supplier on the subject alone (never on a dependency, whose supplier the operator does not speak for), and holds the result to the NTIA minimum elements: the document's author, timestamp and namespace, a subject it describes, and every component's name, version, purl or CPE and a dependency relationship. A dependency's supplier, which a generator rarely knows, is counted and warned about, and fails the task only with strict_suppliers. The SBOM (sbom.spdx.json) and a SHA256SUMS in sha256sum's format are written into dest, a new directory, so sha256sum -c verifies it anywhere; the undo removes the directory. A check generates and validates the SBOM as a run does and writes nothing. The scanner runs in a fresh container on the device's own engine, from an image pinned here by its multi-platform index digest, so nothing is installed on the device and every run uses the scanner this release was tested with; what it scans is mounted read-only, its root is read-only, it runs with no-new-privileges and is removed afterwards. Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket.

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
| `dir` | `string` | no | - | A directory on the device to describe. One of dir and image_archive. |
| `image_archive` | `string` | no | - | An image archive on the device, as docker save or podman save writes one. |
| `dest` | `string` | yes | - | The directory to write, which must not exist, in one that does. |
| `name` | `string` | yes | - | The subject's name: what is being shipped. |
| `version` | `string` | yes | - | The subject's version. |
| `supplier` | `string` | yes | - | Who supplies the subject, as SPDX writes it: "Organization: name" or "Person: name". |
| `strict_suppliers` | `bool` | no | `false` | Fail when a dependency has no supplier, rather than counting it. |
| `timeout` | `string` | no | `15m` | How long the scanner may run, up to 2h, after which its container is killed and removed. |
| `pull` | `bool` | no | `true` | Pull the scanner's pinned image when the engine does not have it. The image is pinned by digest, so a pull cannot substitute another; a device that cannot pull needs it loaded first. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | The directory written. |
| `sbom` | `string` | always | The SBOM's path on the device. |
| `sha256` | `string` | always | The SBOM's SHA-256 in hex, as SHA256SUMS records it. |
| `components` | `int` | always | How many components the SBOM holds, the subject included. |
| `missing_supplier` | `int` | always | How many dependencies have no supplier. |
| `scanner` | `string` | always | The scanner's image, pinned by digest. |
| `warnings` | `list of string` | when there is one | Ignore entries expired or unused, and anything else the run must say. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo removes the directory it created; it never writes into an existing one, so nothing else is lost.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `scan.vuln`
- `file.remove`

## Examples

Describe a release's image:

```yaml
- name: SBOM
  scan.sbom:
    image_archive: /srv/release/app.tar
    dest: /srv/release/sbom-2.4.1
    name: app
    version: 2.4.1
    supplier: "Organization: Example Co"
```


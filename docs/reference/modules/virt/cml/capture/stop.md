---
status: beta
---

# virt.cml.capture.stop

Stops a packet capture on a link of a CML lab and writes its file into the project.

Stops the capture on the link named by link, or the one between a's a_interface and b's b_interface, and, with dest, fetches its pcap file from the controller and writes it into the project at dest (a path under the project directory, never .pleiades; up to 16 MiB, so bound a long capture with packets or seconds). A capture that already stopped at its bound is not stopped again, and its file is still fetched. The file is the packets as they crossed the wire: any cleartext that crossed is in it, so keep it with the project's other private files. Its result names the file, its size and SHA-256. The task's target is a cml_controller device; it writes only on the command line (pleiades run and adhoc), which holds the project. A check reads the capture and reports what it would do, writing nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CMLAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id. |
| `link` | `string` | no | - | The link's id. |
| `a` | `string` | no | - | One node, by label or id, with a_interface, b and b_interface, naming the link by its ends. |
| `a_interface` | `string` | no | - | Its interface, by label. |
| `b` | `string` | no | - | The other node, by label or id. |
| `b_interface` | `string` | no | - | Its interface, by label. |
| `dest` | `string` | no | - | Where in the project to write the capture file, such as captures/core1-wan.pcap. Without it the capture is stopped and its file left on the controller. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | always | The link's id. |
| `packets` | `int` | when a capture was running | How many packets the capture had taken when it was stopped. |
| `dest` | `string` | when dest is set | The file written. |
| `bytes` | `int` | when dest is set | Its size. |
| `sha256` | `string` | when dest is set | Its SHA-256, in hex. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A stopped capture cannot be resumed, and the file it wrote is the record of what crossed the link, which no undo should take away.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.capture.start`
- `net.pcap.summary`


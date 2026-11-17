---
status: beta
---

# virt.cml.capture.stop

Stops a packet capture on a link of a CML lab and writes its file into the project.

Stops the capture on the link named by link, or the one between a's a_interface and b's b_interface, and, with dest, fetches its pcap file from the controller and writes it into the project at dest (a path under the project directory, never .pleiades; up to 16 MiB, so bound a long capture with packets or seconds; a file the controller says is larger is refused with its size before it is read). The controller writes the file behind the capture (CML 2.7 took about four minutes to write 25,593 packets, 9 MB, after the stop, and served it short until then), so the stop waits, up to timeout, until the file is whole: its length held still across one five-second poll, its last record complete, and at least as many packets in it as the capture reported just before this task stopped it. For a capture that had already stopped (at its own packets or seconds bound) that count is not known, and the length must hold still across three polls in a row (fifteen seconds) instead. Each poll reads the length from the headers of a request for the file whose download is then cut off (CML refuses HEAD and serves no byte ranges), and the file is downloaded whole only once its length holds, not on every poll. A capture that stopped at its own bound reports itself running until its file is whole (measured: 15,000 packets taken in 20 seconds, the file written for another 63 seconds at about 70 KB a second without a pause, and the capture reported stopped at the moment it was whole). A download the controller cuts off partway (it resets the HTTP/2 stream when the file grows during one) is asked for again, from where it stopped when the controller serves byte ranges and from the start otherwise, four tries in all; a download is bounded by a minute without a byte, not by its length. A capture that already stopped at its bound is not stopped again, and its file is still fetched. The file is the packets as they crossed the wire: any cleartext that crossed is in it, so keep it with the project's other private files. Its result names the file, its size and SHA-256. The task's target is a cml_controller device; it writes only on the command line (pleiades run and adhoc), which holds the project. A check reads the capture and reports what it would do, writing nothing.

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
| `dest` | `string` | no | - | Where in the project to write the capture file, a name ending in .pcap or .pcapng, such as captures/core1-wan.pcap. Without it the capture is stopped and its file left on the controller. |
| `timeout` | `int` | no | `900` | How many seconds to wait for the controller to finish writing the file, 1 to 86400; it also bounds a check's one fetch. A file still being written then is refused, naming how far it had got and how the last fetch ended, and nothing is written. |

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

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.capture.start`
- `net.pcap.summary`

## Examples

Keep a capture for net.pcap.summary:

```yaml
- name: Stop the capture and keep the file
  virt.cml.capture.stop:
    lab: cvd
    a: CORE1
    a_interface: Ethernet0/1
    b: WAN1
    b_interface: Ethernet0/0
    dest: captures/core1-wan.pcap
```


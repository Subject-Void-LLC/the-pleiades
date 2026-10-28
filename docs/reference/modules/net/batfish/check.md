---
status: beta
---

# net.batfish.check

Checks configurations against Batfish's model of the network before any of them is applied, and fails closed.

Builds a snapshot from each device's whole configuration (config, or src: a file in the project), its secrets masked before anything is sent (equal secrets alike, so key mismatches still show), asks Batfish to parse it, and holds the model to the assertions given: OSPF sessions established, a route present (through the next hops named) or absent, a flow delivered or not and through or around the nodes named, and a node's modelled routes equal to the routes net.frr.facts read from it live. Each scenario forks the snapshot with interfaces or nodes down and holds it to its own assertions, so a link failure is checked before it happens. Its target is the batfish device. FRR configurations go to Batfish in the layout it reads FRR in, with each ip ospf passive moved under router ospf; Cisco IOS ones go as they are. It fails closed: an assertion that does not hold, a file Batfish could not parse, a question it could not answer, a convert error Batfish reports (part of a device missing from the model), or a line it did not recognise that the task does not accept by name with a reason (lines with no meaning of their own, exit, end and !, need none). Recognising a line is not modelling it, so a feature measured to be parsed and then ignored (on Cisco IOS: GLBP, IP SLA, SLA tracking, BFD, NHRP) is refused the same way until accepted by name: the model cannot predict its failover, and a passing gate must not imply that it did. The result reports three things apart: whether the model parsed (each file's status and every unrecognised line), whether it agrees (each assertion), and what it does not model (the lines accepted). It returns the SHA-256 of each configuration as given, for net.frr.config's expect_sha256, so the change applied is the one checked. It changes nothing on any device; Batfish keeps the snapshot in a network of the run's own, removed when the task ends. A model that agrees is evidence, not proof: rehearse on the same OS image, and verify the change live.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `BatfishAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `devices` | `list of dict` | yes | - | Each device's whole configuration: name (the node's name, which a Cisco configuration's hostname must match), format (frr, the default, or cisco_ios), and config (the text, e.g. a registered net.frr.facts result's running_config) or src (a file in the project, read by the command line only). |
| `assertions` | `list of dict` | no | - | What must hold of the configurations as given, each a map with a single key: ospf_established: true (every OSPF session Batfish finds is established, and there is one); bgp_established: true (the same for BGP); route: {node, prefix, next_hops, protocol, vrf}, where protocol is bgp, connected, eigrp, isis, kernel, local, ospf, rip or static (Batfish's own ibgp, aggregate, ospfIA, ospfE1, ospfE2, isisL1, isisL2 and eigrpEX are read as their family, so ibgp is written bgp); no_route: {node, prefix, vrf} (vrf narrows either to one VRF, such as default or a front-door VRF; without it every VRF the model has counts, so a front door's default route is a route to 0.0.0.0/0); reach: {from (node, every interface of it; node[interface], the node sending from that interface's address in its VRF; or @enter(node[interface]), a packet arriving on it from the wire, which Batfish cannot trace for an interface in a VRF; the interface may be quoted, node["Ethernet0/1"]), dst (an address), delivered (default true), through (nodes), avoids (nodes), accepted_by (the node that must answer dst, such as an HSRP or VRRP group's elected owner of a virtual address)}; routes_match: {node, routes (net.frr.facts' routes for that node), ignore (prefixes the configurations do not hold, such as a management network), vrf}, comparing connected, static, OSPF, BGP, IS-IS, EIGRP and RIP routes by prefix, protocol and next hop; question: {name (any Batfish question, e.g. hsrpProperties, filterLineReachability), variables, where (column: value), min_rows (default 1), max_rows}, the rows matching where counted. |
| `scenarios` | `list of dict` | no | - | Each a name, down_interfaces (node[interface], e.g. r1[eth2]) and down_nodes, and its own assertions, checked on the snapshot with those down. |
| `network` | `string` | no | `pleiades` | What the name of the Batfish network this task makes begins with, followed by a hyphen and 16 random hexadecimal digits (a lab's name, such as pleiades-cvd-opus, makes pleiades-cvd-opus-1f3a...), so the networks each lab makes on one shared Batfish service read as that lab's. 1 to 64 letters, digits, dots, hyphens and underscores, beginning with a letter or digit. The random part keeps two runs given the same name apart, and each task deletes only the network it made, when it ends. The whole name is returned as network. |
| `accept_unrecognized` | `list of dict` | no | - | Lines Batfish did not recognise, or features it does not model, that the task accepts: each a text (the line, trimmed; a line holding a secret is matched without it, so ip ospf authentication-key accepts that line) or a feature (glbp, ip_sla, sla_tracking, bfd, nhrp), and a reason (why the model may ignore it). Every other unrecognised line fails the task. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `network` | `string` | once the network is made | The Batfish network the task made and removed when it ended, its name beginning with the network parameter. |
| `passed` | `bool` | always | Whether every assertion held and nothing went unrecognised unaccepted. |
| `sha256` | `dict` | always | Each device's configuration's SHA-256, in hex, over the bytes as given. |
| `parse` | `list of dict` | always | Each file's parse status (device, status). |
| `unrecognized` | `list of dict` | always | Each unrecognised line (device, line, text) and, when accepted, why. |
| `assertions` | `list of dict` | always | Each assertion (scenario, kind, passed, detail). |
| `not_modelled` | `list of string` | always | What the model ignores: each accepted line, with its reason. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It asks Batfish about configurations and changes no device; the snapshot it uploads is removed when the task ends.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.frr.config`
- `net.frr.facts`
- `wait.routes`
- `net.trace`

## Examples

Check a candidate and a link failure first:

```yaml
- name: Read r1 as it runs
  net.frr.facts:
    target: r1
    include_config: true
  register: r1
- name: Read r2 as it runs
  net.frr.facts:
    target: r2
    include_config: true
  register: r2
- name: The model routes both LANs with r3's change, and without r1's link to r2
  net.batfish.check:
    target: batfish
    devices:
      - {name: r1, config: "{{ result.r1.running_config }}"}
      - {name: r2, config: "{{ result.r2.running_config }}"}
      - {name: r3, src: configs/r3.frr.conf}
    assertions:
      - ospf_established: true
      - route: {node: r1, prefix: 10.3.3.0/24}
    scenarios:
      - name: r1-r2 down
        down_interfaces: ["r1[eth1]"]
        assertions:
          - reach: {from: "r1[eth3]", dst: 10.3.3.10, through: [r3]}
    accept_unrecognized:
      - {text: domainname localdomain, reason: "DNS search domain, not routing"}
  register: gate
```


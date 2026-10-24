---
status: beta
---

# Verifying a network change

A routing change that looks right can still black-hole a subnet. The Pleiades checks a change in three
stages, in this order:

1. **The model.** `net.batfish.check` holds the candidate configurations to
   [Batfish](https://www.batfish.org/)'s model of the network before anything is applied. It needs
   only the configurations and a Batfish service, whatever the network runs on: real devices,
   containerlab, GNS3 or CML2.
2. **A rehearsal** on lab devices running the same OS image as production. A model of an FRR
   router says nothing about a Cisco one.
3. **The change**, in a block with `rollback_on_failure: true`, verified live against the model it
   passed. If the verification fails, the block undoes the change by itself.

A model that agrees is evidence, not proof. Batfish models steady state from configuration text. It
does not model runtime state (whether an IP SLA probe's target answers, how fast BFD notices a
failure), nor the features listed under [what the model sees](#what-the-model-sees). That is why
stages 2 and 3 still run after a model passes.

`pleiades doc --create-skill` teaches an agent these stages, together with three rules. An agent
never starts Batfish, or deploys or destroys a lab, without the person's say-so. A missing service
is reported, never skipped. And an unmodelled line is accepted only with a reason the person
would agree with.

## The devices

| Device | Type | How it is reached | What grants its methods |
| --- | --- | --- | --- |
| An FRRouting router | `generic_ssh` | SSH, as an unprivileged account allowed to use `vtysh` (the `frrvty` group) and to write FRR's run directory, `/var/run/frr`, where `frr-reload.py` writes its reload files (for example `chgrp frrvty /var/run/frr; chmod 2775 /var/run/frr`) | `pleiades onboard` grants `FRRCapable` only after `vtysh -c 'show version'` succeeds as that account; `net.frr.config` checks the write access itself before any change, in a check too |
| An Open vSwitch switch | `generic_ssh` | SSH, as an account allowed to use the OVS database | `pleiades onboard` grants `OpenVSwitchCapable` only after `ovs-vsctl show` succeeds as that account |
| A Batfish service | `batfish` | Its REST API (`api_base`, e.g. `http://127.0.0.1:9996`) | Declared by the type: `BatfishAPICapable` |

Each grant proves the account is allowed, not just that the program is installed. A router whose
`vtysh` the account may not run is not granted `FRRCapable`.

Batfish has no authentication. A `batfish` device therefore reaches it over plain HTTP only at a
loopback address, unless its record sets `batfish_allow_plaintext: true` for a private network you
have decided to trust. Every run against such a device then warns. Over HTTPS, the device's own TLS
settings apply (`tls_ca_pem`, `tls_server_name`).

```console
pleiades add-host batfish --type batfish --set api_base=http://127.0.0.1:9996
```

A containerlab lab's nodes come in with one command. It reads the `topology-data.json` containerlab
writes on deploy, and refuses any node that is not the named lab's:

```console
pleiades import-containerlab --topology clab-routing-lab/topology-data.json --lab routing-lab \
  --username netops --key keys/id_ed25519 --trust-first-connect --onboard
```

Each node becomes a `generic_ssh` device at its published SSH port, or else at its management
address. `--trust-first-connect` trusts whatever host keys answer, unverified, as
`trust-host --first-connect` does. A lab makes new host keys on every deploy, at the same addresses, so
they go in a file of the lab's own, never your `~/.ssh/known_hosts`: the import refuses
`--trust-first-connect` unless `PLEIADES_KNOWN_HOSTS` names one (`export PLEIADES_KNOWN_HOSTS=$PWD/known_hosts`),
and every later command reads the same variable.

A lab's end hosts (containerlab's `linux` nodes) often run no SSH. `net.trace` traces from one anyway,
through the engine of the machine the lab runs on, a `container_host` device, as `container.exec` runs a
command: name the container, and the task's target is the container host.

```yaml
- name: h1 reaches h2 through r1
  net.trace:
    target: dockerhost
    container: clab-routing-lab-h1
    dest: 10.3.3.10
    expect:
      reached: true
      includes: [10.1.1.1]
```

### GNS3 and Cisco Modeling Labs

A GNS3 project's nodes come from the server's own API, on a 3.x server (which needs a login) or a 2.2
one:

```console
pleiades import-gns3 --server http://gns3.lab:3080 --project routing-lab --username admin --password-stdin < gns3.pw
```

GNS3 reaches its nodes at their consoles, so each node with a telnet console becomes a `console_device`
at that console. A console on every address of the server is reached at the server's own host. A node with
a VNC, SPICE or no console is skipped, with its console type in the report. A project is named by its name,
which must name one project, or by its id.

A CML lab's nodes come from the controller's API. An IOS or IOS XE node definition (IOL, IOSv, CSR 1000v,
Catalyst 8000v and 9000v) becomes a `cisco_router` or `cisco_switch`; any other a `generic_ssh` device:

```console
pleiades import-cml --server https://10.10.20.161 --lab default-lab --username developer --password-stdin \
  --tls-fingerprint sha256:8e57dd2f... --address R1=10.10.20.171 --address R2=10.10.20.172 < cml.pw
```

A node's address is the one the controller observed on it, or the one `--address` gives. It is never
read out of the node's configuration: a router's configuration names an address for every interface, and
the wrong one would send the node's credential to another machine. The report lists the addresses a
configuration names, as a hint.

A controller's certificate is often its own CA's, and a lab's may have expired. `--tls-fingerprint`
trusts exactly the certificate with that SHA-256 fingerprint, whatever its issuer or dates; `--tls-ca`
trusts a CA, with the name and dates checked. With neither, the system's CAs decide, and a refusal names
the certificate's fingerprint, subject and expiry, so you can check it with the controller's owner before
pinning it.

Both importers refuse a password over plain HTTP to anything but this machine unless `--allow-plaintext`
is given, and then warn. A password is read from standard input (`--password-stdin`) or from
`PLEIADES_GNS3_PASSWORD` or `PLEIADES_CML_PASSWORD`, never from a flag.

### Devices with no SSH

Many lab images, and older devices, answer only Telnet until someone enables SSH on them. Telnet carries
the login and everything after it in cleartext, so a task reaches a device over it only with
`insecure_telnet: true` among the method's parameters, written beside its other parameters
(`net.ios.facts: {insecure_telnet: true}`), not as a task key. A Cisco device reached that way has `transport: telnet` in
its record (`import-cml --via telnet` writes it); `net.ios.*` and `net.cli.*` then run over Telnet, and
`telnet_exec` runs one command.

The device's stored credential answers its login prompt (`Username:` or `login:`, then `Password:`). A
device that asks for a login with no credential stored, or refuses the one given, is refused by name.
A console that asks nothing, such as a GNS3 VPCS, needs no credential.

An account that lands in user mode (`R1>`) runs commands, and `net.ios.config` refuses there. Store the
device's enable secret with its credential, and the IOS methods enter privileged mode with it:

```console
printf 'login-password\nenable-secret\n' | pleiades add-credential R1 --username cisco --password-stdin --enable-password-stdin
```

A wrong enable secret is refused by name, and the secret appears in no result or error.

### Building a lab in CML

A CML controller is a device too, `cml_controller`, holding its base URL, its own TLS settings and its own
login (never a template's machine credential):

```console
pleiades add-host cml --type cml_controller --set api_base=https://10.10.20.161 \
  --set tls_fingerprint=sha256:8e57dd2f...
pleiades add-credential cml --username developer --password-stdin < cml.pw
```

`tls_fingerprint` is a device TLS setting any device can use: it trusts exactly the certificate with that
SHA-256 fingerprint, whatever its issuer, dates or names, and every run against the device warns that it
does. `import-cml --controller cml` then reads the server, its trust and its login from that device, so
the controller's password stays in the vault.

The `virt.cml` methods build and take apart labs through it, one verb each, every one with a check (a lab or node an earlier task in the same run makes is reported
unchecked, since it is not there yet):
`virt.cml.lab.create`, `lab.start`, `lab.stop`, `lab.delete`, `lab.info`, `lab.list`, `node.create`,
`node.delete`, `link.create` and `link.delete`. Each change records its undo, so `pleiades rollback`
removes exactly what a run made: a lab's start is undone by stopping exactly the nodes it started, and a
node or lab delete, which wipes disks, cannot be undone and says so.

`virt.cml.node.create` with `device:` makes a node standing for an inventory device (a `cisco_router` or
`cisco_switch`). Its day-0 configuration sets the hostname to the node's label, puts the device's `host` on
the management interface in the `Mgmt-intf` VRF with a default route to `management_gateway`, enables SSH,
and creates the device's stored login as `username <user> privilege 15 secret 9 <hash>` (and `enable secret
9` when the credential has an enable password). The method is handed only IOS type 9 hashes; the
passwords never reach the runbook or the task's output, and never leave this machine. The hashes do: they
are in the node's configuration, which the controller keeps and anyone who can read the lab can read. A
type 9 hash is scrypt and slow to attack, but it is still a verifier of the password, so give a node the
login of a device that exists only in the lab, never one that shares a credential with real equipment.

```yaml
# What this does: builds a one-router lab, the router standing for the inventory device P1.
# Blast radius: the CML controller cml; one new lab, cvd.
# Changes: a lab, two nodes and a link are made, and both nodes started.
# Undo: pleiades rollback <run-id> stops, then removes, everything it made.
id: build-cvd
hosts: cml
tasks:
  - name: make the lab
    virt.cml.lab.create:
      title: cvd
  - name: the bridge to the management network
    virt.cml.node.create:
      lab: cvd
      label: ext
      definition: external_connector
      configuration: System Bridge
  - name: P1, standing for the inventory device P1
    virt.cml.node.create:
      lab: cvd
      label: P1
      definition: iol-xe
      device: P1
      management_gateway: 10.10.20.254
  - name: link P1's management port to the bridge
    virt.cml.link.create:
      lab: cvd
      a: P1
      a_interface: Ethernet0/0
      b: ext
      b_interface: port
  - name: start the lab and wait for it to boot
    virt.cml.lab.start:
      lab: cvd
```

### Reaching a node through the controller's console

A node with no address reachable from here is reached through the controller's console server: an SSH
login to the controller with its own stored credential, then the node's console line, where the node's own
login is answered. Nothing crosses the network in cleartext, and no route to the node is needed.

```console
pleiades trust-host cml --first-connect                         # the console server's host key
pleiades import-cml --controller cml --lab cvd --via console     # each IOS node, by its console's UUID
pleiades add-credential P1 --username netops --password-stdin < p1.pw
```

Such a node has `transport: console`, `console_server` (the controller device) and `console_line` (the
console's UUID, which names exactly one node, where a lab's title may name two) in its record, and every
`net.ios.*` and `net.cli.*` method reaches it that way. The console server's host key is always checked,
whatever a task says about its own target's, each console connection is a fresh one, and each read on it
has a time limit of its own. When the task ends the line is logged out, so the next person on that console
meets a login rather than this task's privileged prompt, unless the connection is already gone by then (a
task that ran out of time, a dropped network). For that case a node `virt.cml.node.create` seeded logs an
idle console session out itself after five minutes (`exec-timeout 5 0`). The controller resolves only on
the CLI so far; a Runner refuses a console device, and `virt.cml.node.create` with `device:`, whose seed a
Runner cannot resolve either.

### Undoing an IOS change

`net.ios.config` copies the running configuration to a checkpoint file on the device before its lines
(the device's default writable disk or flash, such as `unix:` or `flash:`), and asks the device itself
what the lines changed (`show archive config differences`). No difference reports unchanged and deletes
the checkpoint, so running the same lines again changes nothing. A change keeps a second checkpoint of
the configuration it left, and records its undo as `net.ios.replace` naming the two files and nothing
else, so no configuration text reaches the journal:

```console
pleiades rollback <run-id>
```

returns the device exactly to the first checkpoint through IOS's `configure replace`, then holds the
device to it and deletes both files. It refuses when the device no longer matches the second checkpoint,
since `configure replace` would undo a later change too; `net.ios.replace` with `force: true` replaces
anyway. A block's `rollback_on_failure` undoes the change the same way inside the run. The checkpoint
files stay on the device until the change is undone; delete them with `net.ios.command`
(`delete /force unix:pleiades-3f2a.cfg`) once you no longer need the undo.

A device with nowhere to keep a checkpoint, or no `configure replace`, records no undo, and neither
does `checkpoint: false`. The task warns, and reports under `undo_lines` the configuration lines that
would undo the change, masked: the `no` form of each added line, and each removed line put back. Copy
them into the task's `rollback:` list, putting each masked secret back:

```yaml
  - name: Describe the uplink
    net.ios.config:
      checkpoint: false
      lines:
        - interface GigabitEthernet0/1
        - description uplink to core
    rollback:
      - name: Put the old description back
        net.ios.config:
          checkpoint: false
          lines:
            - interface GigabitEthernet0/1
            - no description uplink to core
            - description spare
```

A change inside an access list, a banner or a certificate has no such lines. `net.cli.config` and a
configuring `net.ios.command` record no undo either; give their tasks a `rollback:` list.

## Running Batfish

Batfish runs as a container on a machine you choose; this runbook runs it on the machine `pleiades`
runs on, through a `container_host` device with no `host` (its local engine socket). Its API is
published on loopback only, since Batfish has no authentication, and it is held to 2 GiB, since the image
otherwise sizes its heap to the machine.

```yaml
# What this does: runs the Batfish service net.batfish.check asks, its API on 127.0.0.1:9996 only.
# Blast radius: this machine's container engine; one new container, batfish. No device is touched.
# Changes: pulls the pinned image if absent, creates and starts the container.
# Undo: pleiades rollback <run-id> removes it.
id: batfish_up
hosts: dockerhost
tasks:
  - name: Batfish runs, its API on loopback only
    container.run:
      name: batfish
      image: batfish/batfish:2026.08.27.3685
      network: bridge
      ports: ["127.0.0.1:9996:9996"]
      memory: 2g
      memory_swap: 2g
  - name: Batfish's API answers
    http.wait:
      url: http://127.0.0.1:9996/v2/version
      timeout: 300
      sleep: 5
```

```console
pleiades add-host dockerhost --type container_host
pleiades onboard dockerhost
pleiades run runbooks/batfish_up.yaml
pleiades add-host batfish --type batfish --set api_base=http://127.0.0.1:9996
```

Batfish takes a minute or two to answer after it starts. `http.wait` waits for it from this machine,
where its port is published; `pleiades.builtin.wait.port` looks from a device over SSH, and
`dockerhost`, with no `host`, has no SSH, so validation refuses it there. An agent never runs this
without the person's say-so.

## The methods

| Method | What it does | Check | Undo |
| --- | --- | --- | --- |
| `net.frr.facts` | A router's version, interfaces, OSPF neighbours, routes and BGP peers, and with `include_config` its running configuration, every secret masked | yes | read only |
| `net.frr.command` | One `show` command; anything else is refused before the router is reached | yes | read only |
| `net.frr.config` | The whole intended configuration, applied through FRR's own `frr-reload.py` | yes | restores the backup it took |
| `net.frr.interface` | An interface administratively up or down | yes | the state it found |
| `wait.routes` | Waits until the routing table holds the routes given, through the next hops given, and has stopped changing | no (a wait) | read only |
| `net.trace` | `tracepath` from a Linux device, or with `container` from a container on a `container_host` through its engine, failing when the path is not the one expected | yes | read only |
| `net.ovs.bridge`, `net.ovs.port` | A bridge and its datapath, a port and its VLAN tag | yes | the state they found |
| `net.batfish.check` | The candidate held to the model, before any change | yes | read only |
| `pleiades.builtin.project.write` | Keeps text a run produced, such as a registered `running_config`, as a file in the project | yes | none (the project is under version control) |

`pleiades doc <method>` prints each one's full contract.

### Keeping a running configuration as a candidate file

A candidate is a whole configuration file in the project, and the natural start is what the router
runs now. `pleiades.builtin.project.write` keeps a registered result as such a file:

```yaml
# What this does: keeps r3's running configuration as a project file to edit into a candidate.
# Blast radius: r3 is read only; one file in the project is written.
# Changes: configs/r3.running.frr.conf in the project.
# Undo: none; the project is under version control.
id: keep_r3
hosts: r3
tasks:
  - name: Read r3 as it runs
    net.frr.facts:
      include_config: true
    register: r3
  - name: Keep it in the project
    pleiades.builtin.project.write:
      path: configs/r3.running.frr.conf
      content: "{{ result.r3.running_config }}"
```

The write acts on no device, so it runs once whatever the runbook's `hosts:` names. Its path is held
to the rules every project file is: relative, inside the project, never through a symbolic link and
never into `.pleiades`. It never replaces the project's inventory or its state file, or writes into an
external Collections directory. The path is the author's: it may render from `vars`
(`configs/{{ vars.site }}.conf`, given with `--extra-vars`), never from a registered result, so a
device's output cannot choose which file is replaced. `mode` may not set an execute bit. The file is written whole and renamed into place, owner-only (`mode` changes
that), and the task reports `changed` only when the bytes or the mode differ. It runs on the command
line, which holds the project; a Walk-tier Runner refuses it.

The configuration it keeps is masked: each secret is a `pleiades-masked-` placeholder, and the task
warns so. That is fine for editing and for the model, and wrong to apply, so `net.frr.config` refuses a
candidate holding a placeholder before the router is touched, and so do `net.cli.config` and
`net.netconf.config`. Put each secret back in the candidate first.

### Applying a configuration

`net.frr.config` takes the whole intended configuration: `config`, or `src`, a file in the project.
A configuration line not in it is removed from the router. Before the router is touched:

- the candidate is checked with `vtysh -m -f`. A line vtysh refuses fails the task, because
  `frr-reload` reads a file vtysh cannot parse as an empty configuration and would delete
  everything;
- `expect_sha256` refuses a candidate whose bytes are not the ones the gate checked.

A real run then saves the running configuration to a backup on the router (mode 0600, under the
connecting account's `~/.pleiades-frr`), reloads, and confirms the router now runs the candidate as
written. FRR silently changes some lines. It keeps only the first eight characters of an OSPF simple
authentication key, for example. A candidate FRR alters is a failure that names the lines, and its
undo is kept.

The undo names only the backup's path, so no configuration text (and no secret) reaches the
journal. It restores exactly what was running before the change. It refuses when the router has
been changed since, unless the undo's step sets `force`. With `save: true`, `frr.conf` is written
too, keeping its owner and mode, and the undo puts it back.

## The gate

`net.batfish.check` runs on the `batfish` device. It takes each device's whole configuration: the
running one (a registered `net.frr.facts` result's `running_config`), or the candidate as a project
file. Every secret is masked before anything is sent. Equal secrets get equal placeholders, so a key
mismatch between two routers still shows.

What it asserts:

| Assertion | Holds when |
| --- | --- |
| `ospf_established: true`, `bgp_established: true` | every session of that protocol the model finds is established, and there is at least one |
| `route: {node, prefix, next_hops, protocol}` | the node has a route to the prefix, through those next hops, by that protocol |
| `no_route: {node, prefix}` | the node has no route to the prefix |
| `reach: {from, dst, delivered, through, avoids, accepted_by}` | a flow from a node, or `node[interface]`, to an address is delivered (or not), every path goes through or around the nodes named, and `accepted_by` names the node that answers, such as an HSRP or VRRP group's elected owner of a virtual address |
| `routes_match: {node, routes, ignore}` | the model's routes for the node equal the routes `net.frr.facts` read from it live, compared by prefix, protocol and next hop for connected, static, OSPF, BGP, IS-IS, EIGRP and RIP routes; `ignore` lists prefixes no configuration holds, such as a management network |
| `question: {name, variables, where, min_rows, max_rows}` | any Batfish question (filters, peers, first-hop redundancy properties) returns between `min_rows` and `max_rows` rows whose cells equal `where` |

A scenario is a fork of the snapshot with interfaces or nodes down (`down_interfaces: ["r1[eth2]"]`,
`down_nodes: [r3]`), checked against its own assertions. That is how a link failure is checked
before it happens.

It fails closed in five cases:

- an assertion does not hold;
- a file did not parse;
- a question could not be answered;
- Batfish reports a convert error, so part of a device is missing from the model, and the task did not accept it by its text;
- a line or feature the model does not use was not accepted by name.

FRR in a container writes `domainname localdomain` into its running configuration when the container's resolver
has that search domain (Docker Desktop gives it one; a plain Linux Docker daemon may not). Batfish does not recognise
the line, so a gate over such routers accepts it: `{text: domainname localdomain, reason: "the DNS search domain,
not routing"}`. Accepting a line a configuration does not hold changes nothing.

The model's result reports three things separately:

- **`parse`:** each file's parse status;
- **`assertions`:** whether the model agrees with each assertion;
- **`not_modelled`:** what the model ignores, each with its reason.

Each configuration's SHA-256 comes back too, for `net.frr.config`'s `expect_sha256`.

## What the model sees

Batfish recognising a line does not mean it models the line. The table below was measured against
the Batfish release this project pins (2026.08.27), not taken from documentation. A line or feature
in the last two rows refuses a gate until the task accepts it in `accept_unrecognized`, by `text`
for a line or by `feature` for a feature, with a reason.

| | FRR | Cisco IOS |
| --- | --- | --- |
| Modelled | connected and static routes, OSPF (including passive interfaces), BGP | the same, HSRP with interface tracking (failover included), VRRP |
| Reported unrecognised | `ip ospf authentication-key` (so a key mismatch is not modelled), `no bgp ebgp-requires-policy`, VRRP | |
| Parsed, then ignored | | GLBP (no forwarder is elected), IP SLA, SLA tracking (an SLA-tracked route never fails over), BFD |

The configuration formats taken are `frr` and `cisco_ios`. Batfish reads Arista EOS and Junos as
they are too. They will be accepted once their device types exist and each format is proven against
real devices.

## An example

```yaml
# What this does: checks r3's candidate against the model, then applies it and verifies it live.
# Blast radius: r3's routing; r1 and r2 are read only.
# Changes: r3's running configuration and frr.conf.
# Undo: the block undoes the change itself if any step after it fails; pleiades rollback undoes it later.
id: r3_change
hosts: r3
metadata:
  service_effecting: true
tasks:
  - name: Read r1
    net.frr.facts: {target: r1, include_config: true}
    register: r1
  - name: Read r2
    net.frr.facts: {target: r2, include_config: true}
    register: r2
  - name: The model routes both LANs with r3's candidate, and without r1's link to r3
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
        - name: r1-r3 down
          down_interfaces: ["r1[eth2]"]
          assertions:
            - reach: {from: "r1[eth3]", dst: 10.3.3.10, through: [r2]}
    register: gate
  - name: Apply and verify, undone if the verification fails
    rollback_on_failure: true
    block:
      - name: r3 runs the checked candidate
        net.frr.config:
          src: configs/r3.frr.conf
          expect_sha256: "{{ result.gate.sha256.r3 }}"
          save: true
      - name: r1 routes r3's LAN through r3 again
        wait.routes:
          target: r1
          prefixes: [10.3.3.0/24]
```

## Limits

- A run holds a device only while one of its tasks works on it, so nothing stops another run changing the
  same router, or a neighbouring one, while a change is verified. Sometimes that is the point (a failure
  injected while a change is verified). The command line notices it and says so, without refusing or
  waiting: a run that changes devices (not a check) records itself under `.pleiades/runs/active` while it
  works, warns as it starts when another live run is changing any of the same devices, and warns as it ends
  naming each other run whose journal records a change to one of its devices while it worked
  (`pleiades journal show <run-id>` lists them). The warnings are in the text view and under `warnings` in
  `--json`. A run on other devices is not named, even a neighbour whose change reaches this one's routes,
  so run one change to a network at a time.

- `src` is read by the command line only. A Walk-tier Runner runs each task against its one
  dispatched device, so a gate that reads several routers' registered facts runs on the command line
  until segmented dispatch is built.
- Masked placeholders are equal for equal secrets within one command-line run. A Runner reads each
  device separately, so there they compare only within one router.
- `net.trace` claims Linux only, and needs iputils `tracepath` on the device or, with `container`, in the
  container (and `/bin/sh`). From a container it needs a `container_host` device whose engine onboarding
  proved, and `pleiades validate` refuses either shape on a device that cannot take it.

---
status: beta
---

# Module catalog

Every registered Collection method, grouped by namespace. A `declared` method is registered but not yet implemented; see each namespace's own page for which methods those are.

| Namespace | Methods | Implemented |
| --- | --- | --- |
| `ai` | 11 | 11 |
| `archive` | 2 | 2 |
| `build` | 3 | 3 |
| `cloud` | 4 | 4 |
| `container` | 37 | 37 |
| `db` | 6 | 6 |
| `edge` | 3 | 3 |
| `exec` | 3 | 3 |
| `facts` | 1 | 1 |
| `file` | 14 | 14 |
| `fs` | 2 | 2 |
| `fw` | 3 | 3 |
| `gate` | 2 | 2 |
| `http` | 3 | 3 |
| `identity` | 6 | 6 |
| `net` | 27 | 25 |
| `pkg` | 9 | 9 |
| `pleiades` | 3 | 3 |
| `scan` | 3 | 3 |
| `svc` | 16 | 16 |
| `virt` | 82 | 82 |
| `wait` | 4 | 4 |
| `win` | 12 | 12 |
| **total** | **256** | **254** |

## All methods

| FQCN | Status | Summary |
| --- | --- | --- |
| [ai.bench.run](ai/bench/run.md) | implemented | Asks a provider's model a prompt set, scores each answer, and reports the score with every call's tokens, time and cost. |
| [ai.chat](ai/chat.md) | implemented | Asks the target provider's model and returns its answer with a usage record: tokens, time, retries and cost. |
| [ai.model.list](ai/model/list.md) | implemented | Lists the models an Ollama host has on disk and the ones in memory. |
| [ai.model.load](ai/model/load.md) | implemented | Loads a model into an Ollama host's memory, unloading every other model first so one is resident at a time. |
| [ai.model.pull](ai/model/pull.md) | implemented | Pulls a model onto an Ollama host when it is not there. |
| [ai.model.remove](ai/model/remove.md) | implemented | Removes a model from an Ollama host, unloading it first when it is in memory. |
| [ai.model.unload](ai/model/unload.md) | implemented | Unloads a model from an Ollama host's memory. |
| [ai.prompt.verify](ai/prompt/verify.md) | implemented | Verifies a signed prompt bundle, parameter set or routing table against the project's trusted keys, as a call would. |
| [ai.provider.list](ai/provider/list.md) | implemented | Reports what a provider is and which models it serves: kind, endpoint, residency, prices and host guard. |
| [ai.provider.probe](ai/provider/probe.md) | implemented | Makes one counted call of a few tokens to a provider's model and reports its latency and usage. |
| [ai.route](ai/route.md) | implemented | Asks a use's providers in fallback order and returns the first answer, refusing any provider the use's residency forbids. |
| [archive.create](archive/create.md) | implemented | Creates an archive (tar or tar.gz) from files on the target. |
| [archive.extract](archive/extract.md) | implemented | Extracts an archive (tar or tar.gz) on the target. |
| [build.digests.check](build/digests/check.md) | implemented | Lists every container image reference in a build's files that is not pinned by both tag and digest. |
| [build.digests.refresh](build/digests/refresh.md) | implemented | Pins every container image reference in a build's files to the digest its tag names now. |
| [build.lock.verify](build/lock/verify.md) | implemented | Checks that a lockfile covers its manifest, carries an integrity hash for every entry, and draws only from allowed sources. |
| [cloud.aws.ec2.create](cloud/aws/ec2/create.md) | implemented | Launches an EC2 instance via the AWS API. |
| [cloud.aws.ec2.terminate](cloud/aws/ec2/terminate.md) | implemented | Terminates an EC2 instance via the AWS API. |
| [cloud.aws.s3.create_bucket](cloud/aws/s3/create_bucket.md) | implemented | Creates an S3 bucket via the AWS API. |
| [cloud.aws.s3.delete_bucket](cloud/aws/s3/delete_bucket.md) | implemented | Deletes an S3 bucket via the AWS API. |
| [container.copy_in](container/copy_in.md) | implemented | Puts one file into a container. |
| [container.copy_out](container/copy_out.md) | implemented | Reads one text file out of a container. |
| [container.create](container/create.md) | implemented | Makes a container exist as declared, without starting it. |
| [container.engine.info](container/engine/info.md) | implemented | Reads the container engine's own report of itself. |
| [container.exec](container/exec.md) | implemented | Runs one command inside a running container. |
| [container.image.import](container/image/import.md) | implemented | Makes an image from a filesystem archive. |
| [container.image.info](container/image/info.md) | implemented | Reports images on the engine. |
| [container.image.load](container/image/load.md) | implemented | Loads images from an archive, refusing one whose digest differs. |
| [container.image.prune](container/image/prune.md) | implemented | Keeps each repository's newest tags and removes the rest. |
| [container.image.pull](container/image/pull.md) | implemented | Pulls an image by digest and names it by its tag. |
| [container.image.push](container/image/push.md) | implemented | Pushes an image to its registry. |
| [container.image.remove](container/image/remove.md) | implemented | Removes an image or one of its tags. |
| [container.image.resolve](container/image/resolve.md) | implemented | Resolves a tag to the digest it names in its registry. |
| [container.image.save](container/image/save.md) | implemented | Saves images to an archive and returns its digest. |
| [container.image.search](container/image/search.md) | implemented | Searches a registry for images. |
| [container.image.tag](container/image/tag.md) | implemented | Names an image by another tag. |
| [container.info](container/info.md) | implemented | Reports one container as the engine sees it. |
| [container.list](container/list.md) | implemented | Lists the engine's containers. |
| [container.logs](container/logs.md) | implemented | Returns a container's last lines of output. |
| [container.network.connect](container/network/connect.md) | implemented | Connects a container to a network. |
| [container.network.create](container/network/create.md) | implemented | Makes a network exist as declared. |
| [container.network.disconnect](container/network/disconnect.md) | implemented | Disconnects a container from a network. |
| [container.network.info](container/network/info.md) | implemented | Reads a network. |
| [container.network.remove](container/network/remove.md) | implemented | Removes a network no container uses. |
| [container.prune](container/prune.md) | implemented | Removes unused containers, images, volumes or networks. |
| [container.recreate](container/recreate.md) | implemented | Replaces a container with its declaration. |
| [container.registry.info](container/registry/info.md) | implemented | Reports the registries the engine's login file holds a login for. |
| [container.registry.login](container/registry/login.md) | implemented | Keeps the bound registry credential in the engine's login file. |
| [container.registry.logout](container/registry/logout.md) | implemented | Removes a registry's login from the engine's login file. |
| [container.remove](container/remove.md) | implemented | Removes a container. |
| [container.restart](container/restart.md) | implemented | Restarts a container. |
| [container.run](container/run.md) | implemented | Makes a container exist as declared, and running. |
| [container.start](container/start.md) | implemented | Starts a stopped container. |
| [container.stop](container/stop.md) | implemented | Stops a running container. |
| [container.volume.create](container/volume/create.md) | implemented | Makes a named volume exist as declared. |
| [container.volume.info](container/volume/info.md) | implemented | Reads a volume. |
| [container.volume.remove](container/volume/remove.md) | implemented | Removes a volume no container uses. |
| [db.pg.dump](db/pg/dump.md) | implemented | Dumps a database with the device's pg_dump into a new archive on the device. |
| [db.pg.grants](db/pg/grants.md) | implemented | Compares the privileges granted to a role with a declared set, and fails naming each extra and each missing one. |
| [db.pg.migrate](db/pg/migrate.md) | implemented | Applies a directory of numbered SQL files in order, each in its own transaction, recording each in a version table. |
| [db.pg.query](db/pg/query.md) | implemented | Runs one SQL statement and returns its rows as CSV, read-only unless the task says otherwise. |
| [db.pg.restore](db/pg/restore.md) | implemented | Restores an archive on the device into a database the task names, in one transaction, refusing a live one unless told why. |
| [db.pg.rowcounts](db/pg/rowcounts.md) | implemented | Counts every table's rows exactly, and with compare fails naming each table whose counts differ from a second database's. |
| [edge.access](edge/access.md) | implemented | Checks a Cloudflare Access application's audience, issuer, settings and policies against what is expected. |
| [edge.dns](edge/dns.md) | implemented | Checks a DNS record set in a Cloudflare zone against what is expected. |
| [edge.tunnel](edge/tunnel.md) | implemented | Checks a Cloudflare Tunnel's status and how many of its connectors are up. |
| [exec.command](exec/command.md) | implemented | Runs one command directly, with no shell involved. |
| [exec.shell](exec/shell.md) | implemented | Runs a command through the target's shell, so pipes and redirects work. |
| [exec.winrm.shell](exec/winrm/shell.md) | implemented | Runs a command on a Windows target through PowerShell, cmd.exe or no shell, over WinRM. |
| [facts.gather](facts/gather.md) | implemented | Gathers baseline system facts from the target (OS, kernel, distribution). |
| [file.block.remove](file/block/remove.md) | implemented | Removes a marked, multi-line block of text from a file. |
| [file.block.set](file/block/set.md) | implemented | Ensures a marked, multi-line block of text is present in a file. |
| [file.copy](file/copy.md) | implemented | Writes content, or a file from the project, to a file on the target, only when the bytes there differ. |
| [file.directory](file/directory.md) | implemented | Makes sure a directory exists on the target, with the mode, owner and group the task asks for. |
| [file.json](file/json.md) | implemented | Sets, or removes, the value at a key path in a JSON file on the device, keeping the rest of the file. |
| [file.line.remove](file/line/remove.md) | implemented | Ensures no line matching a pattern remains in a file. |
| [file.line.set](file/line/set.md) | implemented | Ensures one line matching a pattern is present in a file, replacing or appending it. |
| [file.permissions](file/permissions.md) | implemented | Sets a file's owner, group, and mode on the target. |
| [file.remove](file/remove.md) | implemented | Removes a file or directory from the target. |
| [file.stat](file/stat.md) | implemented | Reports what is at a path on the target, and changes nothing. |
| [file.symlink](file/symlink.md) | implemented | Makes a path a symbolic link pointing at a target, and refuses to replace a real file or directory. |
| [file.template](file/template.md) | implemented | Renders a template with the run's variables and results and writes the result to a file on the target, only when the bytes there differ. |
| [file.touch](file/touch.md) | implemented | Creates an empty file on the target, or updates its modification time. |
| [file.yaml](file/yaml.md) | implemented | Sets, or removes, the value at a key path in a YAML file on the device, keeping its comments and the rest of the file. |
| [fs.mount](fs/mount.md) | implemented | Mounts a filesystem on the target, and optionally persists it to fstab. |
| [fs.unmount](fs/unmount.md) | implemented | Unmounts a filesystem on the target, and optionally removes it from fstab. |
| [fw.firewalld.allow](fw/firewalld/allow.md) | implemented | Opens a port or service in firewalld. |
| [fw.firewalld.deny](fw/firewalld/deny.md) | implemented | Closes a port or service in firewalld. |
| [fw.firewalld.reload](fw/firewalld/reload.md) | implemented | Reloads firewalld to apply pending rule changes. |
| [gate.list](gate/list.md) | implemented | Validates a project's gate manifest on the device and lists how each check is classified, running nothing. |
| [gate.run](gate/run.md) | implemented | Runs a project's gate manifest of named checks on the device and reports PASS, FAIL or SKIP for each. |
| [http.check](http/check.md) | implemented | Checks that a URL answers with the expected status, and optionally body, without ever returning or logging the body. |
| [http.request](http/request.md) | implemented | Makes an HTTP request and reports its status code and body. |
| [http.wait](http/wait.md) | implemented | Waits, from the machine pleiades runs on, until a URL answers with the expected status, and optionally body. |
| [identity.group.create](identity/group/create.md) | implemented | Makes sure a POSIX group exists on the target. |
| [identity.group.modify](identity/group/modify.md) | implemented | Changes the gid of an existing POSIX group on the target. |
| [identity.group.remove](identity/group/remove.md) | implemented | Removes a POSIX group from the target. |
| [identity.user.create](identity/user/create.md) | implemented | Makes sure a POSIX user account exists on the target. |
| [identity.user.modify](identity/user/modify.md) | implemented | Changes attributes of an existing POSIX user account on the target. |
| [identity.user.remove](identity/user/remove.md) | implemented | Removes a POSIX user account from the target. |
| [net.batfish.check](net/batfish/check.md) | implemented | Checks configurations against Batfish's model of the network before any of them is applied, and fails closed. |
| [net.catalyst.device_facts](net/catalyst/device_facts.md) | implemented | Gathers every device a Cisco Catalyst Center manages, as facts. |
| [net.catalyst.reachability](net/catalyst/reachability.md) | implemented | Reports which devices a Cisco Catalyst Center can currently reach and manage. |
| [net.catalyst.site_facts](net/catalyst/site_facts.md) | implemented | Gathers a Cisco Catalyst Center's site hierarchy, as facts. |
| [net.catalyst.tag_facts](net/catalyst/tag_facts.md) | implemented | Gathers the tags defined on a Cisco Catalyst Center, as facts. |
| [net.cli.command](net/cli/command.md) | implemented | Runs one show/exec-mode command against a network device's CLI. |
| [net.cli.config](net/cli/config.md) | implemented | Applies configuration lines to a network device over its CLI. |
| [net.eos.config](net/eos/config.md) | declared | Applies configuration to an Arista EOS device. |
| [net.frr.command](net/frr/command.md) | implemented | Runs one FRRouting show command through vtysh and returns its output. |
| [net.frr.config](net/frr/config.md) | implemented | Makes an FRRouting router run exactly the configuration given, and saves what it ran before so the change can be undone. |
| [net.frr.facts](net/frr/facts.md) | implemented | Reads an FRRouting router's version, interfaces, OSPF neighbours, routes and BGP peers as data. |
| [net.frr.interface](net/frr/interface.md) | implemented | Sets an FRRouting interface administratively up or down, recording the state it found. |
| [net.ios.checkpoints](net/ios/checkpoints.md) | implemented | Lists the checkpoints net.ios.config left on a Cisco IOS device, and with prune deletes the older ones. |
| [net.ios.command](net/ios/command.md) | implemented | Runs one exec-mode command on a Cisco IOS device, answering the questions it asks as the task says. |
| [net.ios.config](net/ios/config.md) | implemented | Applies configuration lines to a Cisco IOS device, keeping a checkpoint of the configuration before them so the change can be undone exactly. |
| [net.ios.facts](net/ios/facts.md) | implemented | Gathers structured facts from a Cisco IOS device over its CLI. |
| [net.ios.parse](net/ios/parse.md) | implemented | Runs a show command on a Cisco IOS or IOS XE device and returns its output parsed into Genie's schema. |
| [net.ios.ping](net/ios/ping.md) | implemented | Runs a ping from a Cisco IOS device and reports the result. |
| [net.ios.replace](net/ios/replace.md) | implemented | Replaces a Cisco IOS device's running configuration with a checkpoint file on the device. |
| [net.ios.save](net/ios/save.md) | implemented | Saves a Cisco IOS device's running configuration to startup. |
| [net.junos.config](net/junos/config.md) | declared | Applies configuration to a Juniper Junos device. |
| [net.netconf.config](net/netconf/config.md) | implemented | Applies a configuration document to a device over NETCONF, with an optional pre-change backup. |
| [net.ovs.bridge](net/ovs/bridge.md) | implemented | Makes an Open vSwitch bridge exist with the datapath given, or not exist. |
| [net.ovs.port](net/ovs/port.md) | implemented | Makes a port exist on an Open vSwitch bridge with the VLAN tag given, or not exist. |
| [net.pcap.summary](net/pcap/summary.md) | implemented | Counts a packet capture in the project by protocol and address pair, encrypted apart from cleartext. |
| [net.ssh.ping](net/ssh/ping.md) | implemented | Opens a real SSH connection to the target and echoes a value back, to prove reachability. |
| [net.trace](net/trace.md) | implemented | Traces the path to an address from a Linux device or a container on a container host, and fails when it is not the path expected. |
| [pkg.apt.install](pkg/apt/install.md) | implemented | Makes sure a package is installed via APT. |
| [pkg.apt.remove](pkg/apt/remove.md) | implemented | Removes a package via APT. |
| [pkg.apt.upgrade](pkg/apt/upgrade.md) | implemented | Makes sure a package is at its newest available version via APT. |
| [pkg.dnf.install](pkg/dnf/install.md) | implemented | Makes sure a package is installed via DNF. |
| [pkg.dnf.remove](pkg/dnf/remove.md) | implemented | Removes a package via DNF. |
| [pkg.dnf.upgrade](pkg/dnf/upgrade.md) | implemented | Makes sure a package is at its newest available version via DNF. |
| [pkg.install](pkg/install.md) | implemented | Makes sure a package is present, whichever package manager the device runs. |
| [pkg.remove](pkg/remove.md) | implemented | Makes sure a package is absent, whichever package manager the device runs. |
| [pkg.upgrade](pkg/upgrade.md) | implemented | Makes sure the newest available version of a package is installed, whichever package manager the device runs. |
| [pleiades.builtin.connection.reset](pleiades/builtin/connection/reset.md) | implemented | Closes the SSH connection kept open to the device, so its next task logs in again. |
| [pleiades.builtin.project.write](pleiades/builtin/project/write.md) | implemented | Writes text a run produced, such as a router's running configuration, to a file in the project. |
| [pleiades.builtin.wait.port](pleiades/builtin/wait/port.md) | implemented | Waits for a TCP port on the target to start (or stop) accepting connections. |
| [scan.sbom](scan/sbom.md) | implemented | Writes an SPDX 2.3 SBOM of an image archive or a directory, held to the NTIA minimum elements, with its SHA256SUMS. |
| [scan.secrets](scan/secrets.md) | implemented | Finds credentials leaked into a directory, naming each by file, line and rule, never by its value. |
| [scan.vuln](scan/vuln.md) | implemented | Lists the known vulnerabilities in an image archive, a directory or lockfiles, and fails at a severity. |
| [svc.disable](svc/disable.md) | implemented | Stops a service starting at boot, whichever service manager the device runs. |
| [svc.enable](svc/enable.md) | implemented | Makes a service start at boot, whichever service manager the device runs. |
| [svc.restart](svc/restart.md) | implemented | Restarts a service, whichever service manager the device runs. |
| [svc.start](svc/start.md) | implemented | Starts a service now, whichever service manager the device runs. |
| [svc.stop](svc/stop.md) | implemented | Stops a service now, whichever service manager the device runs. |
| [svc.systemd.daemon_reload](svc/systemd/daemon_reload.md) | implemented | Makes systemd re-read every unit file on disk. |
| [svc.systemd.disable](svc/systemd/disable.md) | implemented | Stops a systemd unit starting at boot, without stopping it now. |
| [svc.systemd.enable](svc/systemd/enable.md) | implemented | Makes a systemd unit start at boot, without starting it now. |
| [svc.systemd.restart](svc/systemd/restart.md) | implemented | Restarts a systemd unit, starting it if it was not running. |
| [svc.systemd.start](svc/systemd/start.md) | implemented | Starts a systemd unit now, without changing whether it starts at boot. |
| [svc.systemd.stop](svc/systemd/stop.md) | implemented | Stops a systemd unit now, without changing whether it starts at boot. |
| [svc.windows.disable](svc/windows/disable.md) | implemented | Stops a Windows service starting at boot, without stopping it now. |
| [svc.windows.enable](svc/windows/enable.md) | implemented | Makes a Windows service start at boot, without starting it now. |
| [svc.windows.restart](svc/windows/restart.md) | implemented | Restarts a Windows service, starting it if it was not running. |
| [svc.windows.start](svc/windows/start.md) | implemented | Starts a Windows service now, without changing its start type. |
| [svc.windows.stop](svc/windows/stop.md) | implemented | Stops a Windows service now, without changing its start type. |
| [virt.clab.capture.packetflix](virt/clab/capture/packetflix.md) | implemented | Builds Packetflix capture links for a lab's interfaces. |
| [virt.clab.edgeshark.install](virt/clab/edgeshark/install.md) | implemented | Installs Edgeshark on the server's host, unless it runs already. |
| [virt.clab.edgeshark.status](virt/clab/edgeshark/status.md) | implemented | Reads whether Edgeshark, the packet-capture service, runs on the server. |
| [virt.clab.edgeshark.uninstall](virt/clab/edgeshark/uninstall.md) | implemented | Uninstalls Edgeshark from the server's host, if it is installed. |
| [virt.clab.image.list](virt/clab/image/list.md) | implemented | Lists the container images the server's runtime holds. |
| [virt.clab.image.pull](virt/clab/image/pull.md) | implemented | Pulls an image onto the server, unless it is already there. |
| [virt.clab.image.remove](virt/clab/image/remove.md) | implemented | Removes an image from the server, if it is there. |
| [virt.clab.lab.apply](virt/clab/lab/apply.md) | implemented | Changes a running lab to match its on-disk topology, by the difference. |
| [virt.clab.lab.deploy](virt/clab/lab/deploy.md) | implemented | Deploys a lab from a topology, unless it is already deployed. |
| [virt.clab.lab.destroy](virt/clab/lab/destroy.md) | implemented | Destroys a lab, unless it is not deployed. |
| [virt.clab.lab.info](virt/clab/lab/info.md) | implemented | Inspects one lab: whether it exists and its nodes. |
| [virt.clab.lab.interfaces](virt/clab/lab/interfaces.md) | implemented | Lists the interfaces of a lab's nodes. |
| [virt.clab.lab.links](virt/clab/lab/links.md) | implemented | Lists a lab's links with each end's interface, address and state. |
| [virt.clab.lab.list](virt/clab/lab/list.md) | implemented | Lists the labs the containerlab server has deployed. |
| [virt.clab.lab.redeploy](virt/clab/lab/redeploy.md) | implemented | Destroys and deploys a lab again from a topology. |
| [virt.clab.lab.restart](virt/clab/lab/restart.md) | implemented | Makes a lab's nodes restarted. |
| [virt.clab.lab.save](virt/clab/lab/save.md) | implemented | Saves each node's running configuration to its startup configuration. |
| [virt.clab.lab.start](virt/clab/lab/start.md) | implemented | Makes a lab's nodes started. |
| [virt.clab.lab.stop](virt/clab/lab/stop.md) | implemented | Makes a lab's nodes stopped. |
| [virt.clab.lab.topology](virt/clab/lab/topology.md) | implemented | Reads a deployed lab's topology as YAML. |
| [virt.clab.lab.validate](virt/clab/lab/validate.md) | implemented | Validates a lab's on-disk topology without deploying it. |
| [virt.clab.netem.reset](virt/clab/netem/reset.md) | implemented | Clears the netem impairments on a lab node's interface, if it has any. |
| [virt.clab.netem.set](virt/clab/netem/set.md) | implemented | Sets delay, jitter, loss or rate on a lab node's interface, unless it is already so. |
| [virt.clab.netem.show](virt/clab/netem/show.md) | implemented | Shows the netem impairments on a lab node's interfaces. |
| [virt.clab.node.disable_tx_offload](virt/clab/node/disable_tx_offload.md) | implemented | Disables TX checksum offload on a lab node's interfaces. |
| [virt.clab.node.exec](virt/clab/node/exec.md) | implemented | Runs one command in a lab node, with no SSH to the node. |
| [virt.clab.node.logs](virt/clab/node/logs.md) | implemented | Reads the last lines of a lab node's output. |
| [virt.clab.node.pause](virt/clab/node/pause.md) | implemented | Makes one lab node paused, if it is not already. |
| [virt.clab.node.ports](virt/clab/node/ports.md) | implemented | Lists the browser-reachable ports a lab node publishes. |
| [virt.clab.node.restart](virt/clab/node/restart.md) | implemented | Restarts one lab node. |
| [virt.clab.node.ssh_access](virt/clab/node/ssh_access.md) | implemented | Opens a temporary SSH door to a lab node through the server. |
| [virt.clab.node.start](virt/clab/node/start.md) | implemented | Makes one lab node started, if it is not already. |
| [virt.clab.node.stop](virt/clab/node/stop.md) | implemented | Makes one lab node stopped, if it is not already. |
| [virt.clab.node.unpause](virt/clab/node/unpause.md) | implemented | Makes one lab node unpaused, if it is not already. |
| [virt.clab.server.health](virt/clab/server/health.md) | implemented | Reads the API server's health. |
| [virt.clab.server.metrics](virt/clab/server/metrics.md) | implemented | Reads the host's CPU, memory and disk figures the API server reports. |
| [virt.clab.server.version](virt/clab/server/version.md) | implemented | Reads the containerlab version the server runs. |
| [virt.clab.server.version_check](virt/clab/server/version_check.md) | implemented | Asks whether a newer containerlab exists. |
| [virt.clab.ssh_session.list](virt/clab/ssh_session/list.md) | implemented | Lists the temporary SSH sessions the server holds. |
| [virt.clab.ssh_session.terminate](virt/clab/ssh_session/terminate.md) | implemented | Closes a temporary SSH session on the server, if it is open. |
| [virt.clab.topology.generate](virt/clab/topology/generate.md) | implemented | Generates a containerlab topology from a description of its tiers. |
| [virt.clab.veth.create](virt/clab/veth/create.md) | implemented | Creates a vEth pair between two endpoints. |
| [virt.clab.vxlan.create](virt/clab/vxlan/create.md) | implemented | Creates a VxLAN tunnel between a host interface and a remote VTEP. |
| [virt.clab.vxlan.delete](virt/clab/vxlan/delete.md) | implemented | Deletes the VxLAN interfaces containerlab made with a prefix. |
| [virt.cml.capture.start](virt/cml/capture/start.md) | implemented | Starts a packet capture on a link of a CML lab. |
| [virt.cml.capture.stop](virt/cml/capture/stop.md) | implemented | Stops a packet capture on a link of a CML lab and writes its file into the project. |
| [virt.cml.lab.create](virt/cml/lab/create.md) | implemented | Creates an empty CML lab with a title. |
| [virt.cml.lab.delete](virt/cml/lab/delete.md) | implemented | Stops, wipes and deletes a CML lab. |
| [virt.cml.lab.info](virt/cml/lab/info.md) | implemented | Reports one CML lab: its nodes, their interfaces and links, and how many nodes the controller runs. |
| [virt.cml.lab.list](virt/cml/lab/list.md) | implemented | Lists the labs on a CML controller. |
| [virt.cml.lab.start](virt/cml/lab/start.md) | implemented | Starts a CML lab's nodes and waits until they boot. |
| [virt.cml.lab.stop](virt/cml/lab/stop.md) | implemented | Stops a CML lab's nodes. |
| [virt.cml.link.create](virt/cml/link/create.md) | implemented | Links two nodes' interfaces in a CML lab. |
| [virt.cml.link.delete](virt/cml/link/delete.md) | implemented | Removes a link from a CML lab. |
| [virt.cml.link.start](virt/cml/link/start.md) | implemented | Starts a stopped link in a CML lab, as if plugged in again. |
| [virt.cml.link.stop](virt/cml/link/stop.md) | implemented | Stops a link in a CML lab, as if unplugged, leaving both nodes running. |
| [virt.cml.node.create](virt/cml/node/create.md) | implemented | Creates a node in a CML lab, optionally seeded with an inventory device's login and address. |
| [virt.cml.node.delete](virt/cml/node/delete.md) | implemented | Stops, wipes and deletes one node of a CML lab. |
| [virt.fusion.vm.info](virt/fusion/vm/info.md) | implemented | Reports a VMware Fusion VM's power state and its guest's address. |
| [virt.fusion.vm.start](virt/fusion/vm/start.md) | implemented | Starts a VMware Fusion VM with no window, and holds the Mac awake while it runs. |
| [virt.fusion.vm.stop](virt/fusion/vm/stop.md) | implemented | Stops or suspends a VMware Fusion VM. |
| [virt.vbox.snapshot.delete](virt/vbox/snapshot/delete.md) | implemented | Deletes a snapshot of a VirtualBox VM. |
| [virt.vbox.snapshot.restore](virt/vbox/snapshot/restore.md) | implemented | Puts a VirtualBox VM back to a snapshot, discarding its current state. |
| [virt.vbox.snapshot.take](virt/vbox/snapshot/take.md) | implemented | Takes a snapshot of a VirtualBox VM under a name no other snapshot of it has. |
| [virt.vbox.vm.addresses](virt/vbox/vm/addresses.md) | implemented | Reports the addresses a VM's host-only adapters were given, from VirtualBox's DHCP server and from virt.vbox.vm.clone. |
| [virt.vbox.vm.attach_disk](virt/vbox/vm/attach_disk.md) | implemented | Gives a stopped VirtualBox VM a second disk, made new on the host when it is not there yet. |
| [virt.vbox.vm.clone](virt/vbox/vm/clone.md) | implemented | Makes a VM as a linked clone of another's snapshot, seeded with a device's login by cloud-init or a Windows answer file. |
| [virt.vbox.vm.delete](virt/vbox/vm/delete.md) | implemented | Deletes a stopped VirtualBox VM and its disks. |
| [virt.vbox.vm.detach_disk](virt/vbox/vm/detach_disk.md) | implemented | Takes a disk off a stopped VirtualBox VM, deleting it from the host when asked. |
| [virt.vbox.vm.eject_seed](virt/vbox/vm/eject_seed.md) | implemented | Takes a VM's seed out of its DVD drive and deletes it, once its first boot has read it. |
| [virt.vbox.vm.host_keys](virt/vbox/vm/host_keys.md) | implemented | Reads a VM's SSH host keys from what cloud-init printed on its serial console. |
| [virt.vbox.vm.import_disk](virt/vbox/vm/import_disk.md) | implemented | Makes a VirtualBox VM from a disk image on the host, such as Microsoft's Windows Server evaluation VHDX. |
| [virt.vbox.vm.import_ova](virt/vbox/vm/import_ova.md) | implemented | Imports an OVA appliance on a VirtualBox host as a VM, with no network adapter. |
| [virt.vbox.vm.info](virt/vbox/vm/info.md) | implemented | Reports a VirtualBox VM's state, hardware and snapshots. |
| [virt.vbox.vm.install](virt/vbox/vm/install.md) | implemented | Makes a Windows or FreeBSD VM by installing it from its installation ISO, unattended, as a base to clone. |
| [virt.vbox.vm.list](virt/vbox/vm/list.md) | implemented | Lists the VMs on a VirtualBox host, with their state and the address Pleiades gave them. |
| [virt.vbox.vm.log](virt/vbox/vm/log.md) | implemented | Reads the end of a VM's VirtualBox log, optionally only the lines matching a pattern. |
| [virt.vbox.vm.resize](virt/vbox/vm/resize.md) | implemented | Changes a stopped VirtualBox VM's CPUs and memory, by T-shirt size or by count. |
| [virt.vbox.vm.screenshot](virt/vbox/vm/screenshot.md) | implemented | Saves a picture of a running VM's screen as a PNG file on the machine running Pleiades. |
| [virt.vbox.vm.send_keys](virt/vbox/vm/send_keys.md) | implemented | Types into a running VM's console, as its keyboard would. |
| [virt.vbox.vm.start](virt/vbox/vm/start.md) | implemented | Starts a VirtualBox VM with no window. |
| [virt.vbox.vm.stop](virt/vbox/vm/stop.md) | implemented | Stops a VirtualBox VM, by its power button or by cutting its power. |
| [wait.connection](wait/connection.md) | implemented | Waits until the target answers a command over its own connection, SSH or WinRM. |
| [wait.path](wait/path.md) | implemented | Waits for a file path on the target to exist (or stop existing). |
| [wait.routes](wait/routes.md) | implemented | Waits until a router has a route to every prefix given, or with state absent has none left, and its routing table has stopped changing. |
| [wait.search](wait/search.md) | implemented | Waits for a pattern to appear in a file's contents on the target. |
| [win.archive.extract](win/archive/extract.md) | implemented | Extracts a .zip, .tar or .tar.gz already on a Windows host into a folder there, over WinRM. |
| [win.capability.install](win/capability/install.md) | implemented | Installs a Windows capability (a feature on demand) with DISM, run as SYSTEM so Windows Update will serve it. |
| [win.capability.remove](win/capability/remove.md) | implemented | Removes a Windows capability (a feature on demand) with DISM, run as SYSTEM. |
| [win.cpu.topology](win/cpu/topology.md) | implemented | Reports a Windows host's logical processors: which are performance cores, which efficiency cores, and how they group. |
| [win.feature.install](win/feature/install.md) | implemented | Enables a Windows optional feature or role via DISM, including its required parent features. |
| [win.feature.remove](win/feature/remove.md) | implemented | Disables a Windows optional feature or role via DISM. |
| [win.file.copy](win/file/copy.md) | implemented | Copies a file from the project to a Windows host over WinRM and keeps it only if its SHA-256 matches. |
| [win.file.download](win/file/download.md) | implemented | Downloads a file onto a Windows host over HTTPS and keeps it only if its SHA-256 matches. |
| [win.firewall.profile](win/firewall/profile.md) | implemented | Sets, or reports, one Windows Defender Firewall profile over WinRM: whether it is on, its default actions and its logging. |
| [win.firewall.rule](win/firewall/rule.md) | implemented | Makes, changes or removes a Windows Defender Firewall rule over WinRM, or reports one as it is. |
| [win.reboot](win/reboot.md) | implemented | Restarts a Windows host over WinRM and waits until it is back, as ansible.windows.win_reboot does. |
| [win.schtask](win/schtask.md) | implemented | Registers, updates or removes a Windows scheduled task over WinRM, or reports one as it is. |

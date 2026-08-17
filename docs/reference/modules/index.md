---
status: beta
---

# Module catalog

Every registered Collection method, grouped by namespace. A `declared` method is registered but not yet implemented; see each namespace's own page for which methods those are.

| Namespace | Methods | Implemented |
| --- | --- | --- |
| `archive` | 2 | 0 |
| `cloud` | 4 | 0 |
| `container` | 3 | 0 |
| `exec` | 2 | 2 |
| `facts` | 1 | 1 |
| `file` | 11 | 10 |
| `fs` | 2 | 0 |
| `fw` | 3 | 0 |
| `http` | 1 | 1 |
| `identity` | 6 | 0 |
| `net` | 11 | 5 |
| `pkg` | 9 | 0 |
| `pleiades` | 1 | 1 |
| `svc` | 16 | 0 |
| `wait` | 2 | 2 |
| `win` | 2 | 0 |
| **total** | **76** | **22** |

## All methods

| FQCN | Status | Summary |
| --- | --- | --- |
| [archive.create](archive/create.md) | declared | Creates an archive (tar or zip) from files on the target. |
| [archive.extract](archive/extract.md) | declared | Extracts an archive (tar or zip) on the target. |
| [cloud.aws.ec2.create](cloud/aws/ec2/create.md) | declared | Creates an EC2 instance via the AWS API. |
| [cloud.aws.ec2.terminate](cloud/aws/ec2/terminate.md) | declared | Terminates an EC2 instance via the AWS API. |
| [cloud.aws.s3.create_bucket](cloud/aws/s3/create_bucket.md) | declared | Creates an S3 bucket via the AWS API. |
| [cloud.aws.s3.delete_bucket](cloud/aws/s3/delete_bucket.md) | declared | Deletes an S3 bucket via the AWS API. |
| [container.docker.remove](container/docker/remove.md) | declared | Removes a Docker container from the target. |
| [container.docker.run](container/docker/run.md) | declared | Runs a Docker container on the target. |
| [container.docker.stop](container/docker/stop.md) | declared | Stops a running Docker container on the target. |
| [exec.command](exec/command.md) | implemented | Runs one command directly, with no shell involved. |
| [exec.shell](exec/shell.md) | implemented | Runs a command through the target's shell, so pipes and redirects work. |
| [facts.gather](facts/gather.md) | implemented | Gathers baseline system facts from the target (OS, kernel, distribution). |
| [file.block.remove](file/block/remove.md) | implemented | Removes a marked, multi-line block of text from a file. |
| [file.block.set](file/block/set.md) | implemented | Ensures a marked, multi-line block of text is present in a file. |
| [file.copy](file/copy.md) | implemented | Writes inline content to a file on the target, only when the bytes there differ. |
| [file.directory](file/directory.md) | implemented | Makes sure a directory exists on the target, with the mode, owner and group the task asks for. |
| [file.line.remove](file/line/remove.md) | implemented | Ensures no line matching a pattern remains in a file. |
| [file.line.set](file/line/set.md) | implemented | Ensures one line matching a pattern is present in a file, replacing or appending it. |
| [file.permissions](file/permissions.md) | implemented | Sets a file's owner, group, and mode on the target. |
| [file.remove](file/remove.md) | implemented | Removes a file or directory from the target. |
| [file.symlink](file/symlink.md) | implemented | Makes a path a symbolic link pointing at a target, and refuses to replace a real file or directory. |
| [file.template](file/template.md) | declared | Renders a template and writes the result to the target. |
| [file.touch](file/touch.md) | implemented | Creates an empty file on the target, or updates its modification time. |
| [fs.mount](fs/mount.md) | declared | Mounts a filesystem on the target, and optionally persists it to fstab. |
| [fs.unmount](fs/unmount.md) | declared | Unmounts a filesystem on the target, and optionally removes it from fstab. |
| [fw.firewalld.allow](fw/firewalld/allow.md) | declared | Opens a port or service in firewalld. |
| [fw.firewalld.deny](fw/firewalld/deny.md) | declared | Closes a port or service in firewalld. |
| [fw.firewalld.reload](fw/firewalld/reload.md) | declared | Reloads firewalld to apply pending rule changes. |
| [http.request](http/request.md) | implemented | Makes an HTTP request and reports its status code and body. |
| [identity.group.create](identity/group/create.md) | declared | Creates a POSIX group on the target. |
| [identity.group.modify](identity/group/modify.md) | declared | Modifies an existing POSIX group on the target. |
| [identity.group.remove](identity/group/remove.md) | declared | Removes a POSIX group from the target. |
| [identity.user.create](identity/user/create.md) | declared | Creates a POSIX user account on the target. |
| [identity.user.modify](identity/user/modify.md) | declared | Modifies an existing POSIX user account on the target. |
| [identity.user.remove](identity/user/remove.md) | declared | Removes a POSIX user account from the target. |
| [net.catalyst.device_facts](net/catalyst/device_facts.md) | implemented | Gathers every device a Cisco Catalyst Center manages, as facts. |
| [net.catalyst.reachability](net/catalyst/reachability.md) | implemented | Reports which devices a Cisco Catalyst Center can currently reach and manage. |
| [net.catalyst.site_facts](net/catalyst/site_facts.md) | implemented | Gathers a Cisco Catalyst Center's site hierarchy, as facts. |
| [net.catalyst.tag_facts](net/catalyst/tag_facts.md) | implemented | Gathers the tags defined on a Cisco Catalyst Center, as facts. |
| [net.cli.command](net/cli/command.md) | declared | Runs one show/exec-mode command against a network device's CLI. |
| [net.cli.config](net/cli/config.md) | declared | Applies configuration lines to a network device over its CLI. |
| [net.eos.config](net/eos/config.md) | declared | Applies configuration to an Arista EOS device. |
| [net.ios.config](net/ios/config.md) | declared | Applies configuration lines to a Cisco IOS device, with an optional pre-change backup. |
| [net.junos.config](net/junos/config.md) | declared | Applies configuration to a Juniper Junos device. |
| [net.netconf.config](net/netconf/config.md) | declared | Applies configuration to a device over NETCONF. |
| [net.ssh.ping](net/ssh/ping.md) | implemented | Opens a real SSH connection to the target and echoes a value back, to prove reachability. |
| [pkg.apt.install](pkg/apt/install.md) | declared | Installs a package via APT. |
| [pkg.apt.remove](pkg/apt/remove.md) | declared | Removes a package via APT. |
| [pkg.apt.upgrade](pkg/apt/upgrade.md) | declared | Upgrades a package via APT. |
| [pkg.dnf.install](pkg/dnf/install.md) | declared | Installs a package via DNF. |
| [pkg.dnf.remove](pkg/dnf/remove.md) | declared | Removes a package via DNF. |
| [pkg.dnf.upgrade](pkg/dnf/upgrade.md) | declared | Upgrades a package via DNF. |
| [pkg.install](pkg/install.md) | declared | Installs a package using the target's own package manager, whichever it is. |
| [pkg.remove](pkg/remove.md) | declared | Removes a package using the target's own package manager, whichever it is. |
| [pkg.upgrade](pkg/upgrade.md) | declared | Upgrades a package using the target's own package manager, whichever it is. |
| [pleiades.builtin.wait.port](pleiades/builtin/wait/port.md) | implemented | Waits for a TCP port on the target to start (or stop) accepting connections. |
| [svc.disable](svc/disable.md) | declared | Disables a service from starting at boot, using the target's own service manager. |
| [svc.enable](svc/enable.md) | declared | Enables a service to start at boot, using the target's own service manager. |
| [svc.restart](svc/restart.md) | declared | Restarts a service using the target's own service manager, whichever it is. |
| [svc.start](svc/start.md) | declared | Starts a service using the target's own service manager, whichever it is. |
| [svc.stop](svc/stop.md) | declared | Stops a service using the target's own service manager, whichever it is. |
| [svc.systemd.daemon_reload](svc/systemd/daemon_reload.md) | declared | Reloads systemd's unit files, after one on disk has changed. |
| [svc.systemd.disable](svc/systemd/disable.md) | declared | Disables a systemd unit from starting at boot. |
| [svc.systemd.enable](svc/systemd/enable.md) | declared | Enables a systemd unit to start at boot. |
| [svc.systemd.restart](svc/systemd/restart.md) | declared | Restarts a systemd unit. |
| [svc.systemd.start](svc/systemd/start.md) | declared | Starts a systemd unit. |
| [svc.systemd.stop](svc/systemd/stop.md) | declared | Stops a systemd unit. |
| [svc.windows.disable](svc/windows/disable.md) | declared | Sets a Windows service's start type to disabled. |
| [svc.windows.enable](svc/windows/enable.md) | declared | Sets a Windows service's start type to automatic. |
| [svc.windows.restart](svc/windows/restart.md) | declared | Restarts a Windows service. |
| [svc.windows.start](svc/windows/start.md) | declared | Starts a Windows service. |
| [svc.windows.stop](svc/windows/stop.md) | declared | Stops a Windows service. |
| [wait.path](wait/path.md) | implemented | Waits for a file path on the target to exist (or stop existing). |
| [wait.search](wait/search.md) | implemented | Waits for a pattern to appear in a file's contents on the target. |
| [win.feature.install](win/feature/install.md) | declared | Installs a Windows feature or role. |
| [win.feature.remove](win/feature/remove.md) | declared | Removes a Windows feature or role. |

---
status: beta
---

# Implementation status

The full declared-versus-implemented matrix for every registered Collection method. See [Start here](../01-start-here.md#implementation-status) for what this means for the product as a whole.

**59 of 77 methods are implemented.**

## Implemented

- [exec.command](modules/exec/command.md)
- [exec.shell](modules/exec/shell.md)
- [exec.winrm.shell](modules/exec/winrm/shell.md)
- [pkg.install](modules/pkg/install.md)
- [pkg.remove](modules/pkg/remove.md)
- [pkg.upgrade](modules/pkg/upgrade.md)
- [pkg.apt.install](modules/pkg/apt/install.md)
- [pkg.apt.remove](modules/pkg/apt/remove.md)
- [pkg.apt.upgrade](modules/pkg/apt/upgrade.md)
- [pkg.dnf.install](modules/pkg/dnf/install.md)
- [pkg.dnf.remove](modules/pkg/dnf/remove.md)
- [pkg.dnf.upgrade](modules/pkg/dnf/upgrade.md)
- [svc.start](modules/svc/start.md)
- [svc.stop](modules/svc/stop.md)
- [svc.restart](modules/svc/restart.md)
- [svc.enable](modules/svc/enable.md)
- [svc.disable](modules/svc/disable.md)
- [svc.systemd.start](modules/svc/systemd/start.md)
- [svc.systemd.stop](modules/svc/systemd/stop.md)
- [svc.systemd.restart](modules/svc/systemd/restart.md)
- [svc.systemd.enable](modules/svc/systemd/enable.md)
- [svc.systemd.disable](modules/svc/systemd/disable.md)
- [svc.systemd.daemon_reload](modules/svc/systemd/daemon_reload.md)
- [identity.user.create](modules/identity/user/create.md)
- [identity.user.modify](modules/identity/user/modify.md)
- [identity.user.remove](modules/identity/user/remove.md)
- [identity.group.create](modules/identity/group/create.md)
- [identity.group.modify](modules/identity/group/modify.md)
- [identity.group.remove](modules/identity/group/remove.md)
- [file.copy](modules/file/copy.md)
- [file.directory](modules/file/directory.md)
- [file.symlink](modules/file/symlink.md)
- [file.remove](modules/file/remove.md)
- [file.touch](modules/file/touch.md)
- [file.permissions](modules/file/permissions.md)
- [file.line.set](modules/file/line/set.md)
- [file.line.remove](modules/file/line/remove.md)
- [file.block.set](modules/file/block/set.md)
- [file.block.remove](modules/file/block/remove.md)
- [net.ssh.ping](modules/net/ssh/ping.md)
- [net.catalyst.device_facts](modules/net/catalyst/device_facts.md)
- [net.catalyst.site_facts](modules/net/catalyst/site_facts.md)
- [net.catalyst.tag_facts](modules/net/catalyst/tag_facts.md)
- [net.catalyst.reachability](modules/net/catalyst/reachability.md)
- [fw.firewalld.allow](modules/fw/firewalld/allow.md)
- [fw.firewalld.deny](modules/fw/firewalld/deny.md)
- [fw.firewalld.reload](modules/fw/firewalld/reload.md)
- [fs.mount](modules/fs/mount.md)
- [fs.unmount](modules/fs/unmount.md)
- [archive.create](modules/archive/create.md)
- [archive.extract](modules/archive/extract.md)
- [container.docker.run](modules/container/docker/run.md)
- [container.docker.stop](modules/container/docker/stop.md)
- [container.docker.remove](modules/container/docker/remove.md)
- [http.request](modules/http/request.md)
- [pleiades.builtin.wait.port](modules/pleiades/builtin/wait/port.md)
- [wait.path](modules/wait/path.md)
- [wait.search](modules/wait/search.md)
- [facts.gather](modules/facts/gather.md)

## Declared, not yet implemented

Registered and reachable through the real dispatcher. Calling one refuses with an explicit "declared but not implemented" error rather than running.

- `svc.windows.start`
- `svc.windows.stop`
- `svc.windows.restart`
- `svc.windows.enable`
- `svc.windows.disable`
- `file.template`
- `net.cli.command`
- `net.cli.config`
- `net.netconf.config`
- `net.ios.config`
- `net.junos.config`
- `net.eos.config`
- `win.feature.install`
- `win.feature.remove`
- `cloud.aws.ec2.create`
- `cloud.aws.ec2.terminate`
- `cloud.aws.s3.create_bucket`
- `cloud.aws.s3.delete_bucket`


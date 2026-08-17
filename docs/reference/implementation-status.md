---
status: beta
---

# Implementation status

The full declared-versus-implemented matrix for every registered Collection method. See [Start here](../01-start-here.md#implementation-status) for what this means for the product as a whole.

**22 of 76 methods are implemented.**

## Implemented

- [exec.command](modules/exec/command.md)
- [exec.shell](modules/exec/shell.md)
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
- [http.request](modules/http/request.md)
- [pleiades.builtin.wait.port](modules/pleiades/builtin/wait/port.md)
- [wait.path](modules/wait/path.md)
- [wait.search](modules/wait/search.md)
- [facts.gather](modules/facts/gather.md)

## Declared, not yet implemented

Registered and reachable through the real dispatcher. Calling one refuses with an explicit "declared but not implemented" error rather than running.

- `pkg.install`
- `pkg.remove`
- `pkg.upgrade`
- `pkg.apt.install`
- `pkg.apt.remove`
- `pkg.apt.upgrade`
- `pkg.dnf.install`
- `pkg.dnf.remove`
- `pkg.dnf.upgrade`
- `svc.start`
- `svc.stop`
- `svc.restart`
- `svc.enable`
- `svc.disable`
- `svc.systemd.start`
- `svc.systemd.stop`
- `svc.systemd.restart`
- `svc.systemd.enable`
- `svc.systemd.disable`
- `svc.systemd.daemon_reload`
- `svc.windows.start`
- `svc.windows.stop`
- `svc.windows.restart`
- `svc.windows.enable`
- `svc.windows.disable`
- `identity.user.create`
- `identity.user.remove`
- `identity.user.modify`
- `identity.group.create`
- `identity.group.remove`
- `identity.group.modify`
- `file.template`
- `net.cli.command`
- `net.cli.config`
- `net.netconf.config`
- `net.ios.config`
- `net.junos.config`
- `net.eos.config`
- `fw.firewalld.allow`
- `fw.firewalld.deny`
- `fw.firewalld.reload`
- `fs.mount`
- `fs.unmount`
- `win.feature.install`
- `win.feature.remove`
- `archive.create`
- `archive.extract`
- `container.docker.run`
- `container.docker.stop`
- `container.docker.remove`
- `cloud.aws.ec2.create`
- `cloud.aws.ec2.terminate`
- `cloud.aws.s3.create_bucket`
- `cloud.aws.s3.delete_bucket`


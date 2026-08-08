---
status: declared
---

# svc.systemd.daemon_reload

Reloads systemd's unit files, after one on disk has changed.

**Status: declared, not implemented.** Registered with the manifest below, so `pleiades validate` and editor tooling already know about it, but calling it refuses with an explicit "not implemented" error rather than running.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SystemdCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |


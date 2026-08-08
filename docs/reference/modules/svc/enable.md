---
status: declared
---

# svc.enable

Enables a service to start at boot, using the target's own service manager.

**Status: declared, not implemented.** Registered with the manifest below, so `pleiades validate` and editor tooling already know about it, but calling it refuses with an explicit "not implemented" error rather than running.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ServiceManagerCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |


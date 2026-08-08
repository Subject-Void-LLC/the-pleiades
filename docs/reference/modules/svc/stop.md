---
status: declared
---

# svc.stop

Stops a service using the target's own service manager, whichever it is.

**Status: declared, not implemented.** Registered with the manifest below, so `pleiades validate` and editor tooling already know about it, but calling it refuses with an explicit "not implemented" error rather than running.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ServiceManagerCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |


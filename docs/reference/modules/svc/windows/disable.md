---
status: declared
---

# svc.windows.disable

Sets a Windows service's start type to disabled.

**Status: declared, not implemented.** Registered with the manifest below, so `pleiades validate` and editor tooling already know about it, but calling it refuses with an explicit "not implemented" error rather than running.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsServiceCapable` |
| Transports | `winrm` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |


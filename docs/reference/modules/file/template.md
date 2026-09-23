---
status: declared
---

# file.template

Renders a template and writes the result to the target.

**Status: declared, not implemented.** Registered with the manifest below, so `pleiades validate` and editor tooling already know about it, but calling it refuses with an explicit "not implemented" error rather than running.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Not supported: a check run names this task as unchecked |
| Engine version | `>=0.2.0` |


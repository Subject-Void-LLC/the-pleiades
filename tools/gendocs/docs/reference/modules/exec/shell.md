---
status: declared
---

# exec.shell

Runs a command through the target's shell, so pipes and redirects work.

**Status: declared, not implemented.** Registered with the manifest below, so `pleiades validate` and editor tooling already know about it, but calling it refuses with an explicit "not implemented" error rather than running.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ShellExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |


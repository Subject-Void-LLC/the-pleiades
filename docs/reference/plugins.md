---
status: beta
---

# Sync plugins

Every registered inventory sync plugin, read from the live registry `pleiades inventory sync` resolves `--plugin` against. Run `pleiades inventory plugins` for the same list from a live binary. A `declared` plugin is registered but its methods return a not-implemented error; only an `implemented` plugin has run against its real upstream system.

| Name | Description | Read-only | Status | Origin |
| --- | --- | --- | --- | --- |
| `catalyst_center` | reads managed network devices from a Cisco Catalyst Center | yes | `implemented` | generated |
| `static_yaml` | reads a static hosts.yaml inventory file | no | `implemented` | hand-written, predates the Forge |

2 sync plugins registered.

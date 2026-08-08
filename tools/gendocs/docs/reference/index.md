---
status: beta
---

# Reference

Generated from the same registries the engine, `pleiades validate`, and the real dispatcher
read. Never hand-edited; regenerate with `go generate ./tools/gendocs` after changing
`internal/forge/catalogdata` or a Collection method's `Manifest.Doc` block.

- [Module catalog](modules/index.md)
- [Capability vocabulary](capabilities.md)
- [Device types](devices.md)
- [Sync plugins](plugins.md)
- [Runbook and task keys](task-keys.md)
- [Implementation status](implementation-status.md)
- [Runbook JSON Schema](schemas/runbook.schema.json)
- [Inventory JSON Schema](schemas/inventory.schema.json)
- [Module catalog](schemas/module-catalog.json) (data, not a schema: every FQCN's full Manifest)

---
status: beta
---

# Reference

Generated from the registries the engine, `pleiades validate`, and the real dispatcher
read. Never hand-edited; regenerate by running `go run ./tools/gendocs` from the repository
root after changing `internal/forge/catalogdata` or a Collection method's `Manifest.Doc`
block.

**One exception, stated plainly.** The `cisco_router` and `linux_server` rows on the
[device types](devices.md) page are typed into the generator, not read from the device
registry, because both types predate the Forge. A test in the generator fails if either
row stops matching what a real binary hydrates, so those two rows cannot drift in
silence. They are still the one place on these pages where a human wrote the data.

- [Module catalog](modules/index.md)
- [Capability vocabulary](capabilities.md)
- [Device types](devices.md)
- [Sync plugins](plugins.md)
- [Runbook and task keys](task-keys.md)
- [Implementation status](implementation-status.md)
- [CLI reference](cli.md)
- [Runbook JSON Schema](schemas/runbook.schema.json)
- [Inventory JSON Schema](schemas/inventory.schema.json)
- [Module catalog](schemas/module-catalog.json) (data, not a schema: every FQCN's full Manifest)
- [OpenAPI document](schemas/openapi.json) (also served live at `/api/v1/openapi.json`)

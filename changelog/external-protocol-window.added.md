An external Collection names the protocol versions it speaks, and The Pleiades takes the highest one both sides
speak, so a program keeps loading across a protocol change for at least one minor release. `pleiades version`
and `runner version` name the versions they speak (both take `--json`), the Controller serves them at
`/.well-known/pleiades/extension-protocols.json`, and `pleiades collection list --describe` names each approved
program's agreed version.

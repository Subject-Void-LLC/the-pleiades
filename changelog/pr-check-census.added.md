`make pr-check` also runs the packages that validate existing runbooks (`cmd/pleiades`, `cmd/runner`,
`internal/adapters/native`, `internal/forge/playbook`) whenever a branch changes validation, the engine, the
collection contract, a method, a device type or an example, and says which file made it do so.

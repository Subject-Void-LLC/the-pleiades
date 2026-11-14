`net.netconf.config` with no `datastore` now writes running only where the device offers `:writable-running`, and
otherwise its candidate, always locked, validated and committed; the parameter no longer defaults to running
(`param-default-changed module-catalog.json#/net.netconf.config/params/datastore/default`). `commit: false` validates the
edit and discards it, reporting no change, instead of leaving it staged, and is refused on running; `lock: never` is
refused on the candidate.

`net.netconf.config` with no `datastore` now writes running only where the device offers `:writable-running`, and
otherwise its candidate, always locked, validated and committed; `commit: false` validates the edit and discards it,
reporting no change, instead of leaving it staged, and is refused on running; `lock: never` is refused on the candidate.

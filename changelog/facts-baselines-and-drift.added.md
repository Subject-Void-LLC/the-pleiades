`pleiades facts baseline` records a named, sealed baseline of each host's stable facts, from the host itself or a golden
host (`--from`), and `pleiades facts drift` reports each key added, changed or removed since, exiting 3 when a host drifted.
The store records no absence, so a key the latest gather no longer confirms (a package uninstalled since a host's own
baseline) is listed as unconfirmed, not removed, and is not drift; removed is only a golden baseline's key the host never
reported.

`net.netconf.config` checks a task (`pleiades run --mode check`, or `check_mode: true`) when it sets `candidate_check:
true`: the edit is staged in the locked candidate datastore, validated and discarded, and the candidate proven as it was.
It runs from the command line only, on a Netopeer2 server, the one measured to qualify, and is reported unchecked with
its reason on a device without `:candidate` or `:validate` and after an earlier task of the same check predicted a
change on that device.

A run or ad-hoc call that changes devices now notices another run changing the same devices: it warns as it
starts when another live run is working on any of them, and as it ends names each other run whose journal
records a change to one of them meanwhile, with the run id `pleiades journal show` takes. Nothing is refused
or made to wait, since a second change can be deliberate. The warnings are printed in the text view and kept
in the `--json` report's new `warnings` list (`kind`, `when`, `message`, and the other run's `run_id`,
`runbook`, `pid` and shared `devices`). A check records and warns about nothing.

Added the `not_run` task status to the `run` and `adhoc` report: a run that validation refused, that a failure
stopped, or that was aborted now lists every planned task it never reached in `tasks`, with a `reason` naming
why (such as `the run stopped after tasks[2] [batfish] failed`), so a reader no longer compares `plan` against
`tasks` to learn whether a step ran.

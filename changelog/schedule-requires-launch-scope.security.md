Writing a schedule now requires permission to launch what the schedule launches: `runbook:execute` for
a job template, `project:write` for a project. Previously `schedule:write` alone was enough, so a token
that could not run a template by hand could still arrange for it to run repeatedly and unattended. The
Schedules form only offers things you may launch, and the API answers 403 naming the scope that is
missing.

A project sync can now be scheduled. Pick a project on the Schedules form the same way you pick a job
template, and its source is fetched on the recurrence you give it. A sync that collides with one
already running is recorded as skipped with the reason `already_running`, and the schedule moves on to
its next occurrence.

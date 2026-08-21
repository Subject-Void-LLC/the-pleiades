Added the scheduler: an RFC 5545 recurrence attached to a template, so automation runs
without somebody pressing launch. Create and edit one in the Schedules view or over
`/api/v1/schedules`, with its own IANA time zone, its own start, and any number of
exclusion rules.

Recurrence matches AWX rather than approximating it. The engine is tested against
occurrence vectors generated from python-dateutil, the library AWX itself schedules on,
across daylight saving transitions in both hemispheres, a half-hour-offset zone, leap
days, month-end rules, ordinal weekdays such as the last Friday, BYSETPOS, and
exclusion rules that straddle a transition. A daily rule keeps its local hour across a
transition, which means two consecutive runs are sometimes 23 or 25 hours apart, and
that is the behavior an operator expects rather than a defect.

`POST /api/v1/schedules/preview` expands a rule without saving it and returns each
occurrence in both local and UTC time, so intent can be confirmed before a schedule
goes live. `GET /api/v1/zoneinfo` lists every zone a schedule may name.

The grammar is a bounded subset, refused when a schedule is saved rather than when it
runs: SECONDLY, BYWEEKNO, BYYEARDAY, BYSECOND and RDATE are not supported, and neither
is a rule naming a date that never occurs, such as 30 February. A rule that cannot be
expanded safely never reaches the database, because one reaching the scan loop would
affect every schedule in the deployment rather than only its own.

Occurrences missed while nothing was running are coalesced. Exactly one run happens on
recovery, for the most recent missed occurrence, and every earlier one is recorded as
skipped with a reason, readable at `GET /api/v1/schedules/{id}/occurrences`. An hourly
job that missed four hours launches once, not sixteen times, and the runs that did not
happen are rows rather than gaps.

A schedule fires at most once per occurrence even while controller replicas are failing
over. Leader election decides which replica scans; a unique database index on the
schedule and the occurrence time is what makes the firing single, claimed before
anything is launched.

A scheduled run reaches devices through the same path a manual launch does, so it
inherits template resolution, credential binding, durable delivery, per-device locking
and the audit trail. A template bound to a credential that prompts for an input at
launch cannot be scheduled, because that value is never stored and so could never be
replayed unattended.

Deleting a template that a schedule still launches now answers 409 and says so, rather
than succeeding and silently stopping the schedule.

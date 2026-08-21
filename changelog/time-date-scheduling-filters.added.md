Added 25 time, date and scheduling filters, callable from `when`, `when_or`,
and `when_cel`: epoch/RFC 3339 and Windows FileTime conversion, timezone
shifting, date arithmetic (add/subtract, delta in seconds or days, round to
hour), human-readable duration rendering, uptime/boot-time conversion,
past/future/older-than/expiring-within predicates, day/week/month boundary
functions, `isLeapYear`, `dayOfWeek`, `isBusinessHour` and
`isMaintenanceWindow`, and `cronNextRun`/`cronPreviousRun` (built on the
existing hand-rolled cron parser, not a scheduling mechanism of this
platform's own). See the generated filter reference for the full list.

Every runbook dispatch is now published on its own per-device message subject
(`pleiades.jobs.dispatch.<device>`) instead of one flat subject shared by the whole fleet.
Anything watching the old `pleiades.jobs.dispatch` subject directly, such as an operator
running `nats sub` or a monitoring rule, needs to watch `pleiades.jobs.dispatch.>` instead.

Runners are unaffected and need no configuration change: they share the same consumer group
they always did, and it now covers the whole subject range, so a dispatch still reaches
exactly one Runner.

Job log and result subjects are unchanged in shape, but the job identifier inside them is
now normalized the same way, so a live log stream opened by an older client against a
running controller should be reopened rather than assumed to still match.

This is what makes it possible to grant a Runner access to one device's work rather than to
the whole fleet. Granting it is a later change; this one makes the permission expressible.

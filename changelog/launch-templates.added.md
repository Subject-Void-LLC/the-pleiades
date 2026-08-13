Added Templates: the saved, reusable definition of something this platform can run.
AWX calls it a Job Template and Semaphore a Task Template, and the sentence both build
around is the one this is shaped by: a template defines what to run, where to run it,
and how to run it.

Before this, launching was four scalars in a query string. There was no way to save the
pairing of a runbook and a target, no way to vary it deliberately, and no record of the
decisions somebody made about how to run it.

A template declares which of its fields a launch may override. Everything else is locked
to what the template was saved with, and a launch that supplies a locked field is told so
by name rather than having it silently applied or silently dropped: applying it would be
a privilege escalation, dropping it would be a lie about what ran.

The kind of thing a template runs is an open registry rather than a fixed list. A native
runbook and an unconverted Ansible playbook are both launchable, they carry the same
surveys, access and history, and they reach different execution adapters. Adding a kind
is one file plus one line, not an edit to every consumer.

Surveys ask a launching operator for values, using Ansible's own question types so a
survey imported from AWX means the same thing here. A password answer is encrypted at
rest through the same envelope encryption device credentials use, and reads back as a
redaction marker rather than its value.

Jobs now record which template, inventory and organization they came from. That last one
closes a gap: the tenancy column on a job had existed since the dispatcher was built with
nothing anywhere writing it, because a free-text group name has no tenant to inherit.

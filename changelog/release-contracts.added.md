The stability page now states each contract a release keeps (runbook schema, module catalog, facts, run report,
API, rollback planning, template and variable semantics, run journal and dispatch payload), and `make
schema-compat` fails a change that breaks a generated schema unless a `.breaking.md` fragment names it. Journal
lines carry a `format` and dispatches a `payload_version`, so a build refuses a newer one by name; a journal
written by 0.4.0 is not readable by 0.3.0.

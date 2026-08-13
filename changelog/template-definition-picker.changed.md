The Templates form now offers what a template runs instead of asking for it, and the
playbook kind actually works.

What a template runs is chosen from a picker over everything the deployment can
resolve, runbooks and playbooks in one list, mirroring AWX's own auto-populated
Playbook dropdown. The KIND control is gone: it is derived from the choice and shown
as a badge. A definition is also verified to resolve when a template is created, over
the API as well as the form, so a reference that names nothing is refused with a 400
at authoring time instead of surfacing later as a failed job.

The playbook kind previously could not work end to end by any path: nothing listed
playbooks, the fan-out resolved every job through the runbook source regardless of
kind, no binary composed the legacy adapter or the kind router, and the reference
grammar templates validated against was rejected outright by the resolver. All four
are fixed. A playbook is named by its project-relative path, exactly as AWX stores it
(`tripplite_python/tripplite_config.yml`), resolved under a root with confinement
checked twice; both binaries read `PLAYBOOK_DIR` exactly as they read `RUNBOOK_DIR`, the
runner composes the kind router over both adapters when `PLAYBOOK_DIR` and
`ANSIBLE_RUNNER_IMAGE` are both set, and the fan-out prepares each job through its own
kind's source. A deployment that has never run Ansible configures none of this and
loses nothing: the picker offers runbooks alone, and creating or dispatching a
playbook is refused with a reason rather than guessed at.

A playbook template saved before this change carried a definition no resolver ever
accepted, so there is nothing runnable to migrate: recreate it from the picker.

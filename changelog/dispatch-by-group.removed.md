Removed `POST /api/v1/jobs/dispatch`, which launched a runbook against a free-text
group name. Launch a template instead: `POST /api/v1/templates/{id}/launch`.

A group name had no tenant, so a job launched that way belonged to no organization,
recorded no saved definition, and could only ever reach the native executor. A
template names an inventory, an inventory belongs to an organization, and the kind it
declares is what routes the dispatch, so all three of those become properties of the
job rather than gaps in it.

A job's API representation changes with it: `group_name` is replaced by `template`,
`template_name`, `inventory`, `organization` and `kind`. The Jobs view no longer
carries a launch form, and the Runbooks view's Run action is now "Create template",
which saves the runbook against an inventory so it can be launched from one place.

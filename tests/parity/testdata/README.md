# The parity corpus

Real response bodies from a production Ascender (AWX) deployment, committed
verbatim. These define correctness for `.SPECIFICATION/AWX_PARITY_ROADMAP.md`
rather than describing it: a field appearing here is a field a customer
actually has, and `representability_test.go` fails until somebody has decided
what we do with it.

Do not reshape a captured response before committing it. A corpus somebody had
to edit is a corpus that can be edited wrongly, and the loader accepts both AWX
response shapes (a `count`/`results` list, or a bare object from a detail
endpoint such as `survey_spec`) precisely so no editing is needed.

## What is here

| Directory | AWX endpoint |
|---|---|
| `job_templates/` | `/api/v2/job_templates/` |
| `projects/` | `/api/v2/projects/` |
| `project_updates/` | `/api/v2/projects/{id}/project_updates/` |
| `credential_types/` | `/api/v2/credential_types/` |
| `survey_specs/` | `/api/v2/job_templates/{id}/survey_spec/` |
| `schedules/` | `/api/v2/schedules/` |
| `activity_stream/` | `/api/v2/activity_stream/` |

## Sub-resources not yet captured

A Project's `related` block names endpoints whose shapes matter to Phase A1 and
are not in the corpus yet. Recorded here so they are known absences rather than
forgotten ones:

- `/organization/`: the single Organization the project belongs to.
- `/teams/`: Teams granted explicit RBAC permissions on this project.
- `/inventories/`: Inventories that use this project as an SCM source, which is
  inventory-from-git. We have no inventory source concept at all.
- `/notification_templates_started|success|error/`: notification templates that
  fire on sync start, success and failure. Phase C3.
- `/object_roles/`: the structural roles AWX offers per object (`admin_role`,
  `update_role`, `use_role`). Our RBAC is viewer/operator/admin at a scope
  target, so the mapping between the two vocabularies is an A1 design decision
  rather than a rename.
- `/access_list/`: which users hold which access to this specific project.

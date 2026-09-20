A schedule now names what it launches with `unified_job_template`, the id of a job template or of a
project, rather than with `template`. The old field still works on a write and is deprecated; it is no
longer returned. A schedule response now carries `unified_job_template`,
`unified_job_template_name` and `unified_job_template_type` in its place, and an occurrence carries
`unified_job_type` saying whether the run it started was a job or a project sync.

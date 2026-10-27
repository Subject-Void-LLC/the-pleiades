`pleiades doc filters` lists every filter a task parameter's `{{ }}` template may use, with what each does, the roots a
template reads and the Jinja2 filters left out on purpose; `pleiades doc conditions` lists the condition forms, their
roots and every function a condition may call, both with `--json`, read from the engine itself. Templates and conditions
read a new `device` root, the device the task runs on (its name, id, tags, capabilities, state and the properties its type
reads, never a secret); a task reading it is decided and rendered on each device, and a target may not read it.
`pleiades doc` takes its flags after the method or topic too.

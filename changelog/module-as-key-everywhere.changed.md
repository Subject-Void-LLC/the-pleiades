Runbooks are written with the method as the task's key (`pkg.apt.install: {name: nginx}`), everywhere Pleiades writes
one: `forge migrate-playbook` now emits that form for every converted task (it wrote `fqcn:` and `params:`), the
sample `pleiades init` writes uses it, and the examples and docs teach only it. `pleiades validate` warns, without
failing, on each task still written as `fqcn:` and `params:`, which the engine continues to read.

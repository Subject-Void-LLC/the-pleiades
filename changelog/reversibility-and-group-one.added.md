Ten more Collection methods are implemented: `file.copy`, `file.line.set`, `file.line.remove`,
`file.block.set`, `file.block.remove`, `wait.path`, `wait.search`,
`pleiades.builtin.wait.port`, `facts.gather` and `http.request`. The catalog reads 22 of 76.
`file.template` is deliberately still declared, and its page says why: the render engine lives
under `internal/` and a Collection may only import `pkg/`, so implementing it needs that engine
moved rather than a second one written.

**How a method describes being undone has changed, and the new shape is the useful one.** A
manifest used to name an inverse method and the prior-state keys a rollback would feed it. That
could not be right, because the true inverse depends on what a run finds rather than on what the
method is: starting a service that was already running must undo to nothing, not to a stop, and
the old shape would have told a rollback to stop something the run never touched.

So a manifest now answers only whether a method can ever be undone (`reversible`, plus a required
reason when it cannot), and a run that changes something records the concrete instruction that
reverses it: an `inverse` stat holding the method to call and the parameters to call it with,
already resolved from the state that run found. A run that changed nothing records nothing, which
is how it says that undoing it means doing nothing. `file.directory` shows why this matters: one
that created a directory records a removal, while one that only fixed a mode records the old mode
and never a removal.

Nothing performs a rollback yet. The recording exists now because only the forward run can capture
the values an undo needs, and they are gone once the change is applied.

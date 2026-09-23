Every built-in Collection method now declares the engine release it ships in rather than
`>=1.0.0`, which no release before 1.0.0 could have met. A release build would have refused
the entire catalog, and `pleiades forge new-collection` would have scaffolded methods with
the same fault.

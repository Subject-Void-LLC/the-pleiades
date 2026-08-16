Helm chart values are quoted where they render into YAML scalars. An
unconstrained value such as `externalDatabase.existingSecret` could previously
carry a newline and add fields to the object it was rendered into: a crafted
value added `optional: true` to the `secretKeyRef` supplying `DB_DSN`, which
turns a missing Secret from a startup failure into a silent fallback to the
controller's default local SQLite database. Numeric values are deliberately left
unquoted, since Kubernetes rejects a quoted port or replica count.

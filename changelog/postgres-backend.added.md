The Controller can now store its state in PostgreSQL, not only in an embedded SQLite
file. Point it at a server with `DB_DSN=postgres://user:pass@host:5432/db?sslmode=disable`.

Both backends run the same versioned migrations and are held to one shared conformance
suite, so neither is a second-class path. SQLite remains available, either as
`DB_DSN=sqlite://./controller.db` or through the older `DB_PATH` variable, which still
works and still means SQLite. Setting both `DB_DSN` and `DB_PATH` is a startup error
rather than a silent preference for one of them.

Two honest notes. The migration generator now takes the dialect as its first argument
(`go run internal/ent/migrate/gen/main.go postgres <name>`), and a schema change has to
be generated for every dialect. And the `docker-compose.yml` in this repository has been
setting a `DB_DSN` no code read until now, so a compose deployment was quietly running
on a SQLite file inside the container while the PostgreSQL service beside it sat unused.

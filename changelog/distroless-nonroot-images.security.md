The published controller and runner images now run as an unprivileged user on a
distroless base that carries no shell and no package manager, and both the build image
and the runtime image are pinned by digest as well as by tag. This also fixes the
controller image failing during its first database migration whenever `DB_DSN` was left
at its SQLite default, which the previous Alpine build caused by compiling without cgo.

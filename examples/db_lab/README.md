# PostgreSQL with the db.pg methods

The db.pg methods do PostgreSQL work against a database a host of yours reaches: back it up and prove the
backup restores, move its schema forward, read it, and hold a role's privileges to what you declared.

## What is here

- `pleiades/inventory.yaml` names one Linux server, `db-host`, that reaches your database: the database
  server itself or a host beside it. `db.pg.dump` and `db.pg.restore` run its `pg_dump` and `pg_restore`, which
  must be at least the server's major version; the other methods need nothing installed there.
- `pleiades/runbooks/backup-verify.yaml` dumps `app`, restores the dump into a scratch database, and compares
  every table's row count between the two.
- `pleiades/runbooks/migrate.yaml` applies `/srv/app/migrations` (files named `<version>_<name>.sql`) as the
  role `app_owner`, refusing a destructive statement a file does not explain.
- `pleiades/runbooks/report.yaml` reads a count as the `reporting` login and checks that role's privileges.

## Connections

A task names a connection; the host, login and password live in the project's encrypted credential file:

```sh
pleiades add-database-connection appdb --host 127.0.0.1 --user app --sslmode disable --live app --password-stdin
pleiades add-database-connection reporting --host 127.0.0.1 --user reporting --sslmode disable --password-stdin
pleiades list-database-connections
```

The host is the database as `db-host` sees it, since every connection is made from there. TLS is
`verify-full` unless the server is local to `db-host` (loopback or a socket); pin a private authority with
`--ca-file`. `--live app` makes `db.pg.restore` refuse to overwrite `app` unless a task says why.
`db.pg.query` refuses a superuser login, which a read-only transaction does not confine, so `reporting`
should be a role with only the privileges a report needs.

## Running it

```sh
cd pleiades
pleiades validate runbooks/backup-verify.yaml runbooks/migrate.yaml runbooks/report.yaml
pleiades run runbooks/migrate.yaml --mode check   # reads and checks every file, lists what is pending, applies nothing
pleiades run runbooks/migrate.yaml
pleiades run runbooks/backup-verify.yaml
pleiades rollback <run-id>                         # removes the archive the dump wrote; a restore is not undone
```

A check of `backup-verify.yaml` in one run fails at the restore, because a checked dump writes no archive for
the restore to read; check each step against an archive that exists.

## What this example has been run against

`pleiades validate` accepts all three runbooks in the repository's own tests. They target a machine of yours,
so they have not been run as written; `cmd/pleiades`'s database release gate runs the same methods through the
real binary against a real SSH server and a real PostgreSQL, checking first that a check changes nothing.

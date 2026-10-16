# Design note: the `db.pg` collection

Status: built (2026-10-15). PostgreSQL work a run needs to do against a database it does not host: read it, back it up,
restore a copy, move its schema forward, and prove two databases or a role's privileges are what they should be.

## Methods

| Method | Does | Site, device |
|---|---|---|
| `db.pg.query` | Runs one SQL statement and returns its rows as CSV, with the column names and a row count. Read-only unless `write: true`: the statement runs in a read-only transaction with a statement timeout and a lock timeout, and more rows than `max_rows` fail the task rather than being cut. | target, required |
| `db.pg.dump` | Runs the device's `pg_dump` (custom format) into a new file on the device, and returns its size and SHA-256. | target, required |
| `db.pg.restore` | Runs the device's `pg_restore` from an archive on the device into a database the task names, in one transaction. A database marked live is refused unless the task overrides it and says why; a database that already holds tables is refused unless `clean: true`. | target, required |
| `db.pg.migrate` | Applies a directory of numbered `.sql` files on the device in order, each in its own transaction, recording each in a version table, as a named role. With `additive_only`, a file that drops, truncates, deletes, renames or retypes anything is refused before any file runs. | target, required |
| `db.pg.grants` | Compares the privileges a role holds in a database (database, schemas, tables, sequences, functions, columns, memberships and ownership) with a declared set, and fails naming each extra and each missing one. | target, required |
| `db.pg.rowcounts` | Counts every table's rows in a database and, with `compare`, in a second one (on the same or another connection), each from one consistent snapshot, and fails naming each table whose counts differ or that only one side has. | target, required |

## Where it runs, and how it reaches the database

The task's device is a host that reaches the database: the database server itself, or a host beside it. Every
method connects **from the device**, never from the machine running pleiades, so the database's own network
policy, `pg_hba.conf` and firewall see the connection they already allow, and a database that listens only on
its host's loopback or Unix socket is reachable at all.

- `query`, `grants`, `rowcounts` and `migrate` speak the PostgreSQL protocol in-process, over a TCP connection the
  device opens for them (an SSH `direct-tcpip` channel, `remoteexec.Conn.DialTCP`), or to the server's Unix socket
  when the connection's host is a directory (`direct-streamlocal`, `DialUnix`). Nothing runs on the device, no
  client is installed there, and the password never leaves the process.
- `dump` and `restore` run the device's own `pg_dump` and `pg_restore`, since a logical dump is theirs to make.
  The password reaches them on standard input, read by the shell into `PGPASSWORD` and exported to that one
  process: never in a command line, never in a file. Every other `PG*` variable is cleared first, so the
  login's own `PGSERVICE` or `PGHOST` cannot redirect the run.
- `migrate` reads its files from the device over the same SSH session (`remotefile.ReadText`), bounded.

The protocol client is `github.com/lib/pq`, already this module's PostgreSQL driver, confined to a new
`pkg/pgclient` (the one `pkg/` package allowed to import it, held by `internal/archtest`). `pkg/pgclient` closes the
driver's three fallbacks onto the machine running pleiades, each with a test that plants the file and proves it
unread: an empty password reading `~/.pgpass`, a missing client certificate reading `~/.postgresql/postgresql.crt`,
and a missing root certificate reading `~/.postgresql/root.crt`; it never reads `PG*` environment variables.

## Connections, by reference

A task names a **connection** (`connection: appdb`), never a host, user or password. A connection is: a host as
the device sees it (a name, an address, or a Unix socket directory), a port, a user, a password (optional, for
peer or trust authentication over a socket), a TLS mode and pinned CA, and the databases on it that are **live**.

- **Crawl:** `pleiades add-database-connection <name> --host --port --user --password-stdin [--sslmode]
  [--ca-file] [--live db,...]` stores it encrypted in the credential file's own section, which no device name can
  address, beside registry accounts. `pleiades list-database-connections` shows names, hosts, users and live databases, never a password;
  `remove-database-connection` removes one.
- **Walk:** a managed credential type, `pleiades_postgres` (kind `database`), with the same inputs plus `name`.
  A template may bind several, each a different `name` (the rule vault credentials already follow, by their
  identifier); the run carries the ones its template binds (a task's `connection` may be rendered, so which it
  names is known only when the task runs), on the dispatch payload beside the registry credential, and the
  Runner hands each to the method that asks for it by name (`sdk.DatabaseConnections`). External Collections never
  receive one, as they never receive a registry credential.
- The password is added to the run's masking set, as a registry password is.

**TLS.** `verify-full` is the default for a TCP host that is not loopback, against the pinned `ca_pem` or the
system roots, built by `pkg/devicetls` as a device's TLS is. `disable` is accepted for loopback and sockets; for
any other host it needs `insecure_transport: true` on the connection and is warned about on every run, as a
device's legacy TLS is. There is no mode that encrypts without verifying: a self-signed server is verified by
pinning its own certificate as `ca_pem`. `pg_dump` and `pg_restore` check a certificate against the host they
dial, so for them a `server_name` works only when the host is an address (dialed as `hostaddr`).

**The credential kind.** The kind vocabulary was AWX's twelve, held closed because the binding rule keys on
kind. `database` is a thirteenth, this platform's own (AWX has no kind for it, so an import is unaffected),
rather than a reuse of `cloud`, which would make a Postgres connection clash with an AWS credential on one
template. Like `vault`, it binds once per name rather than once per kind.

**Live databases.** A database is live when its connection lists it in `live`, or when the database says so
itself (`ALTER DATABASE app SET pleiades.live = on`, readable by anyone who can connect). Either is enough.
`restore` refuses a live target unless the task sets `allow_live: true` and a non-empty `live_reason`, which the
result records and the run warns about.

## Capabilities

| Method | Capabilities | Why |
|---|---|---|
| `query`, `grants`, `rowcounts` | `SSHTransport` | a tunnel from the device; nothing runs there |
| `migrate` | `CommandExecCapable` | reads the migration files over SSH, then a tunnel |
| `dump`, `restore` | `CommandExecCapable` | runs `pg_dump`/`pg_restore` and `timeout -k` on the device |

## Check and Reversibility

| Method | Check | Reversible |
|---|---|---|
| `db.pg.query` | Read-only: same as Invoke. With `write: true`: unchecked (`CannotCheck`, decided from the params, so `check_mode` on such a task is refused at validate): running a write and rolling it back is not "changing nothing" (sequences advance, locks are taken, triggers and `dblink` act outside the transaction). | Read-only calls change nothing. A write: no, it runs arbitrary SQL. |
| `db.pg.dump` | Connects, checks `pg_dump`'s major version against the server's, that `dest` is absent and its directory exists; writes nothing. | Yes: the undo is `file.remove` of the archive it created (it never replaces one). |
| `db.pg.restore` | Lists the archive (`pg_restore --list`), and makes every refusal a run makes (live, non-empty, absent without `create`); writes nothing. | No: it replaces a database's contents. Dump the target first to keep a way back. |
| `db.pg.migrate` | Reads and lints every file, reads the version table, verifies every applied file's checksum, and reports the pending ones; applies nothing. | No: SQL has no inverse the method can derive. Write the reverse as a `rollback:` list, or a new forward migration. |
| `db.pg.grants` | Same as Invoke (it only reads). | Read-only. |
| `db.pg.rowcounts` | Same as Invoke (it only reads). | Read-only. |

## Decisions, by edge case

- **Connect from the device, not the platform.** A database usually trusts its own host and network, not a laptop
  or a Runner; going through the device is what the operator already permits, and is how a socket-only database is
  reached. A database reachable only from the platform is not covered (no device), and is listed below.
- **A read-only transaction is not a sandbox, so `query` refuses a privileged role.** `COPY ... TO PROGRAM`,
  `pg_read_file`, `lo_export`, `pg_terminate_backend` and `dblink` all act from inside a read-only transaction. A
  role that is a superuser, or a member of `pg_execute_server_program`, `pg_read_server_files` or
  `pg_write_server_files`, is refused unless the task sets `allow_privileged_role: true`, which the run warns
  about. The role's own privileges are the boundary; the docs say to give queries a read-only role. `grants`,
  `rowcounts` and `migrate` send only SQL the method wrote (or the operator's own migration files), so the rule is
  `query`'s alone.
- **One statement.** `query` prepares the statement (the extended protocol), which the server refuses for more than
  one, so `SELECT 1; DROP TABLE t` is an error rather than a read followed by a write.
- **Rows past `max_rows` fail, never truncate.** A report that grows past its bound must say so rather than drop
  its last rows quietly; `LIMIT` in the SQL is how to ask for fewer. `max_rows` defaults to 1,000 (up to
  100,000); the CSV is at most 1 MiB. A NULL is an empty field; an empty string is `""`. There is no `dest`:
  writing a file would make a read a change, and a read's check would no longer be the read itself.
- **Every statement has a timeout.** `query` defaults to 30 seconds and a 5-second lock timeout (a read queued
  behind a schema change would otherwise queue everything behind it); `migrate` defaults to 15 minutes per file
  and the same lock timeout, so a busy table fails the migration instead of stalling the application; `dump` and
  `restore` run under `timeout -k` on the device, default one hour.
- **`migrate` is forward-only and strict.** A file name is `<number>_<name>.sql`; another `.sql` file in the
  directory is refused (a typo is not silently skipped), as is a `.down.sql` file (it would otherwise run as an
  up). Versions are ordered numerically and must be unique. A file whose recorded checksum no longer matches is
  refused, as is a pending version below the newest applied one unless `allow_out_of_order: true`. A file that
  cannot run in a transaction (`CREATE INDEX CONCURRENTLY`) starts with `-- pleiades: no-transaction` and must
  hold exactly one statement, so a failure cannot leave half of it applied. A transactional file may not commit
  or roll back itself. `up_to` applies pending versions up to one.
- **Concurrent runs are serialized by the version key, not a lock.** This repository's own migrations settle
  concurrency that way (`internal/archtest` refuses an advisory lock anywhere, since one slow or dead holder
  stops everyone), and the same reasoning holds here: each file claims its version as the table's primary key
  first, in its own transaction, so a second run applying the same file waits on the claim (up to `claim_wait`)
  and then reads the first's record instead of applying it again. A no-transaction file's claim is committed
  first with `finished = false`, marked finished when its statement succeeds and removed when it fails, so a run
  that died midway leaves a row later runs name rather than guess past. Creating the version table concurrently
  is tolerated (two sessions' `CREATE TABLE IF NOT EXISTS` can meet as a duplicate catalog key).
- **The additive-only lint is a lexer, not a regex.** It reads comments, quoted identifiers, standard and escape
  strings and dollar quoting, so `DROP` inside a string or a comment is not a statement. `UPDATE` is allowed
  (a backfill is additive); a file that must be destructive says so with `-- pleiades: destructive: <reason>`,
  which the result records. The lexer is fuzzed.
- **`grants` compares grants made to the role, not everything it can do.** Privileges reached through `PUBLIC` or
  another role's membership are that grant's to answer for; ownership is listed as its own entry and is extra
  unless declared, and an owned object lacks no privilege, since an owner holds everything on it. A column grant is reported, never folded into its table.
  Default privileges (`ALTER DEFAULT PRIVILEGES`) are not compared yet.
- **`rowcounts` counts exactly.** `count(*)` per table in a `REPEATABLE READ READ ONLY` transaction per side, so
  each side is one moment; the planner's estimates are not a verification. Partitions are counted through their
  parent, once. Comparing a live database with a copy of it will differ by whatever was written since; the
  comparison is for copies at rest.
- **`dump` never replaces an archive.** A new file per run (`dest` must not exist), written beside its final name
  and hard-linked to it (`ln`, which fails rather than replace a name that appeared meanwhile), so a failed dump
  never leaves a truncated archive under the name a restore would use. A filesystem without hard links fails the
  dump and keeps nothing. The undo is a plain removal, recorded as soon as the archive is in place, so a failure
  reading it back is a warning rather than a failure that would drop the undo.

## Reuse

`pkg/remoteexec` (the tunnel and the commands), `pkg/remotefile` (`ReadText`, `Stat`, `Checksum`), `pkg/sdk`
(warnings, inverses), the registry credential's whole path for connections (Crawl file section and command,
credential type and binding rule, dispatch payload, Runner child), `pkg/devicetls`'s rule for weak transport, and
`gate.run`'s `timeout -k` wrapper. New: `pkg/pgclient` (the confined driver, a read-only query with bounds, a
consistent-snapshot reader) and `pkg/sqllint` (the SQL lexer and the additive-only rule).

## Not built

- A database reached from the platform itself, with no device: needs a connection-only call (`DeviceCall`), and a
  Runner reaching a customer's database directly is a different exposure; later, by decision.
- Default privileges in `grants`; `pg_dumpall` (roles and tablespaces); a dump streamed off the device.

## Failure modes

| Failure | Answer |
|---|---|
| The connection is not defined | Refused before contact, naming the command (Crawl) or the credential type (Walk) that defines one. |
| The device's SSH server refuses forwarding (`AllowTcpForwarding no`) | Refused naming that setting; nothing ran. |
| TLS without verification to a remote host, no `insecure_transport` | Refused before the password is sent. |
| `query` returns more than `max_rows` | Fails naming the bound and `LIMIT`; nothing is returned. |
| `query` as a privileged role | Refused before the statement runs, naming the role's privilege. |
| A statement times out or waits past `lock_timeout` | Fails with the server's own message; the transaction is rolled back. |
| `pg_dump` older than the server's major version | Refused before dumping (`pg_dump` would refuse too, after connecting). |
| `dest` exists | Refused; a dump never replaces an archive. |
| `restore` into a live database, or a non-empty one without `clean` | Refused before `pg_restore` runs, naming the marker or the tables found. |
| `pg_restore` fails partway | Nothing is left: it runs in one transaction. The error carries its last lines, bounded and masked. |
| A migration file fails | That file's transaction rolls back, later files do not run, and the error names the file, its statement and the server's message; files before it stay applied and recorded. |
| An applied migration's file changed | Refused before anything runs, naming the file and both checksums. |
| Another run is applying the same version | This run waits on its claim (up to `claim_wait`), then reads its record: applied with the same checksum is a warning, a different checksum or a failed claim is an error. |
| A no-transaction file's run died midway | Its claim stays unfinished; a later run waits `claim_wait`, then fails naming the row to delete. |
| The run fails | The engine keeps a task's returns only when it succeeds, so the error carries what a failure needs (the offending rows, privileges, tables or files), bounded. |

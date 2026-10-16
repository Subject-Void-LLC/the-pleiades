---
status: beta
---

# db.pg.migrate

Applies a directory of numbered SQL files in order, each in its own transaction, recording each in a version table.

Reads every .sql file in dir on the device, named <version>_<name>.sql and applied in numeric order. Another .sql name, in any letter case (1_x.SQL), is refused rather than skipped, as is a .down.sql file (this applies forward only) and a version two files share. Every file is read and checked before any runs: a file may not commit or roll back its own transaction, and a statement that cannot run in a transaction (CREATE INDEX CONCURRENTLY, VACUUM) needs a file of its own starting -- pleiades: no-transaction, holding exactly that one statement. With additive_only, a file that drops, truncates, deletes, revokes, renames or changes a column's type is refused unless it says why (-- pleiades: destructive: <why>), which the result lists; the check is a SQL lexer, so a keyword in a string or comment does not count. Each file runs in its own transaction, as role when given, with a statement timeout and a lock timeout (a busy table fails the migration rather than stalling the application), and its version, name, SHA-256 and duration are recorded in the version table in the same transaction. An applied file whose checksum changed is refused, as is a pending version below the newest applied one unless allow_out_of_order is set. Runs against one version table are serialized by the version itself, with no lock: each file claims its version as the table's primary key before it runs, so a second run applying the same file waits on the first's claim and then reads its record rather than applying it again; a no-transaction file's claim is recorded first as unfinished and marked finished once its statement succeeds (removed if it fails), so a run that died midway is named rather than guessed at. The lint reads a backslash in a string literal as the standard does, so before each file is sent the session must have standard_conforming_strings on, and a file that turns it off stops the run before the next. A check reads and checks every file, reads the version table and reports the pending files, applying nothing. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `connection` | `string` | yes | - | The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them. |
| `database` | `string` | yes | - | The database to connect to. |
| `dir` | `string` | yes | - | The directory of migration files on the device. |
| `table` | `string` | no | `public.pleiades_migrations` | The version table, as schema.name; created when missing, owned by role when given. |
| `role` | `string` | no | - | The role each file runs as (SET ROLE), so it owns what the file creates. The connection's login must be a member. |
| `additive_only` | `bool` | no | `false` | Refuse a destructive statement in a file that does not say why. |
| `allow_out_of_order` | `bool` | no | `false` | Apply a pending version below the newest applied one. |
| `up_to` | `int` | no | - | Apply pending versions up to this one only; 0 is a version, so it applies nothing above version 0. |
| `timeout` | `string` | no | `15m` | Each file's statement timeout, up to 24h. |
| `lock_timeout` | `string` | no | `5s` | How long a statement may wait for a lock, up to 10m. |
| `claim_wait` | `string` | no | `15m` | How long to wait for another run applying the same version to finish, up to 24h. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `applied` | `list of dict` | after a run | Each file applied: version, name and duration_ms. |
| `pending` | `list of dict` | always | Each file still pending (in a check, the files a run would apply): version and name. |
| `destructive` | `list of dict` | always | Each file that says it is destructive: file and reason. |
| `warnings` | `list of string` | when there is one | What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

SQL has no inverse the method can derive; write the reverse as a rollback: list, or as a new forward migration.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `db.pg.query`
- `db.pg.grants`

## Examples

Apply the application's migrations:

```yaml
- name: Migrate
  db.pg.migrate:
    connection: appdb
    database: app
    dir: /srv/app/migrations
    role: app_owner
    additive_only: true
```


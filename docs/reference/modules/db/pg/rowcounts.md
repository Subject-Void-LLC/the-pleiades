---
status: beta
---

# db.pg.rowcounts

Counts every table's rows exactly, and with compare fails naming each table whose counts differ from a second database's.

Counts the rows of every table outside the system schemas with count(*), in a REPEATABLE READ READ ONLY transaction per database, so each side's counts are one moment; partitions are counted through their parent, once. With compare, a second database (on the same connection, or another named one, reached through the same device) is counted the same way and the two are compared table by table: a different count, or a table only one side has, fails the task. Comparing a live database with a copy of it differs by whatever was written since; the comparison is for copies at rest, such as a restore checked against the dump's source taken at the same time. Planner estimates are never used. A database that does not exist holds no tables and is reported as exists false, in a run and in a check; with compare, a side that does not exist is a difference that fails the task, so two that do not exist never agree. It only reads, so a check of it is the task itself. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SSHTransportCapable` |
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
| `compare` | `dict` | no | - | The database to compare with: database, and connection when it is on another server. Any other key is refused. |
| `schemas` | `list of string` | no | - | Count only the tables in these schemas, each of which must exist. |
| `exclude` | `list of string` | no | - | Tables not to count, as schema.table or schema.*. |
| `max_tables` | `int` | no | `1000` | The most tables one side may count, up to 10000; past it the task fails rather than count part. |
| `timeout` | `string` | no | `5m` | Each count's statement timeout, up to 1h. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `tables` | `list of dict` | on success | Each table counted: table, rows, and compare_rows with compare. |
| `differences` | `int` | on success | How many tables' counts differ, which is zero for a task that succeeded. |
| `only_here` | `list of string` | on success | Tables only the first database has. |
| `only_there` | `list of string` | on success | Tables only the compared database has. |
| `exists` | `bool` | always | Whether the database exists; one that does not is counted as holding no tables. |
| `compare_exists` | `bool` | with compare | Whether the compared database exists; one that does not is a difference. |
| `warnings` | `list of string` | when there is one | What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It counts rows in read-only transactions, and writes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `db.pg.restore`
- `db.pg.query`

## Examples

Check a restored copy against its source:

```yaml
- name: Restore matches
  db.pg.rowcounts:
    connection: appdb
    database: app_restore_check
    compare:
      database: app
```


---
status: beta
---

# db.pg.query

Runs one SQL statement and returns its rows as CSV, read-only unless the task says otherwise.

Prepares exactly one statement (the server refuses more than one in a prepared statement) and runs it in a transaction with a statement timeout and a lock timeout set for that transaction alone. Without write, the transaction is read-only and rolled back; with write, it is committed, and a data change without RETURNING is run for the count of rows it changed. The rows are returned as RFC 4180 CSV with a header row, NULL as an empty field and the empty string as ""; more than max_rows rows, or more than 1 MiB of CSV, fails the task rather than return part. A read-only transaction stops writes to the database's tables but is not a sandbox: a superuser, or a member of pg_execute_server_program, pg_read_server_files or pg_write_server_files, can still act outside the database (COPY TO PROGRAM, server files), so such a login is refused unless allow_privileged_role is set, which the run warns about. Give queries a role of their own with only the privileges they need. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SSHTransportCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported for some calls, named in the description: those report what they would change and change nothing, a check run names any other as unchecked, and validation refuses check_mode on one |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `connection` | `string` | yes | - | The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them. |
| `database` | `string` | yes | - | The database to connect to. |
| `sql` | `string` | yes | - | One SQL statement, with $1, $2, ... for args. |
| `args` | `list` | no | - | Values for $1, $2, ..., sent as parameters, never written into the SQL. |
| `write` | `bool` | no | `false` | Run in a read-write transaction and commit it. A task with write cannot be checked (check_mode is refused at validation). |
| `timeout` | `string` | no | `30s` | The statement timeout, as a duration such as 30s or 5m, up to 1h. |
| `lock_timeout` | `string` | no | `5s` | How long the statement may wait for a lock, up to 10m: a read queued behind a schema change would otherwise hold up everything queued behind it. |
| `max_rows` | `int` | no | `1000` | The most rows the task returns, up to 100000; past it the task fails. Use LIMIT in the SQL to ask for fewer. |
| `allow_privileged_role` | `bool` | no | `false` | Allow a login that a read-only transaction does not confine (a superuser, or a member of pg_execute_server_program, pg_read_server_files or pg_write_server_files). The run warns about it. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `columns` | `list of string` | when the statement returns rows | The column names, in order. |
| `row_count` | `int` | when the statement returns rows | How many rows it returned. |
| `csv` | `string` | when the statement returns rows | The rows as CSV with a header row. |
| `rows_affected` | `int` | for a write without RETURNING | How many rows it changed. |
| `warnings` | `list of string` | when there is one | What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

With write it runs arbitrary SQL, whose effect the method cannot invert; write the reverse as a rollback: list. Without write it changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `db.pg.rowcounts`
- `db.pg.grants`

## Examples

Count a table's recent rows:

```yaml
- name: Orders today
  db.pg.query:
    connection: appdb
    database: app
    sql: SELECT count(*) AS orders FROM orders WHERE created_at > now() - interval '1 day'
  register: today
```

Look one row up by a value:

```yaml
- name: Find the account
  db.pg.query:
    connection: appdb
    database: app
    sql: SELECT id, status FROM accounts WHERE email = $1
    args: ["{{ vars.email }}"]
```


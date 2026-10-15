---
status: beta
---

# db.pg.restore

Restores an archive on the device into a database the task names, in one transaction, refusing a live one unless told why.

Runs the device's pg_restore with --single-transaction and --exit-on-error, so a restore that fails leaves nothing of itself behind. The target database is named by the task, never defaulted. A database is live when its connection lists it, or when it says so itself (ALTER DATABASE app SET pleiades.live = on): restoring over one is refused unless allow_live is set with a live_reason, which the result records and the run warns about. A database that does not exist is refused unless create is set, in which case it is created first and dropped again if the restore fails; one that already holds tables, views or sequences is refused unless clean is set (--clean --if-exists). By default objects are restored owned by the connection's login and without the archive's grants (no_owner, no_privileges), which suits a copy restored for verification; turn both off to restore a database as it was. The archive is listed (pg_restore --list) before anything changes, which refuses a file that is not one. A check makes every refusal a run makes and writes nothing. It cannot be undone: dump the target first to keep a way back. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.

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
| `database` | `string` | yes | - | The database to restore into, named explicitly. |
| `src` | `string` | yes | - | The archive's absolute path on the device, as db.pg.dump writes it. |
| `create` | `bool` | no | `false` | Create the database when it does not exist (dropped again if the restore fails). |
| `clean` | `bool` | no | `false` | Restore into a database that already holds objects, dropping each the archive restores first. |
| `allow_live` | `bool` | no | `false` | Allow restoring over a live database. Needs live_reason. |
| `live_reason` | `string` | no | - | Why a live database is being overwritten, recorded in the result and warned about. |
| `no_owner` | `bool` | no | `true` | Restore objects owned by the connection's login rather than the archive's owners (--no-owner). |
| `no_privileges` | `bool` | no | `true` | Leave out the archive's grants (--no-privileges). |
| `maintenance_database` | `string` | no | `postgres` | The database connected to while the target is checked, created or dropped. |
| `timeout` | `string` | no | `1h` | How long pg_restore may run, up to 24h, after which it is stopped on the device and its transaction rolled back. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `database` | `string` | always | The database restored into. |
| `created` | `bool` | always | Whether it was created (or, in a check, would be). |
| `toc_entries` | `int` | always | How many entries the archive lists. |
| `live_override` | `string` | when a live database was overwritten | The live_reason the task gave. |
| `server_version` | `int` | always | The server's major version. |
| `tool_version` | `string` | always | pg_restore --version on the device. |
| `warnings` | `list of string` | when there is one | What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It replaces a database's contents, which no recorded value could put back; dump the target first (db.pg.dump) to keep a way back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `db.pg.dump`
- `db.pg.rowcounts`

## Examples

Restore last night's dump into a scratch database:

```yaml
- name: Restore to scratch
  db.pg.restore:
    connection: appdb
    database: app_restore_check
    src: /var/backups/app.dump
    create: true
```


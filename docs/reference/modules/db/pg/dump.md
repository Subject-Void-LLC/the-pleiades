---
status: beta
---

# db.pg.dump

Dumps a database with the device's pg_dump into a new archive on the device.

Runs pg_dump on the device in its custom format into dest, which must not exist: a dump never replaces an archive. The archive is written beside dest and linked to it once complete, so a failed or interrupted dump never leaves a partial archive under the name a restore would read. pg_dump must be at least the server's major version, which is checked before anything is written. The password reaches pg_dump on standard input, read by the shell into PGPASSWORD for that process alone, never on a command line or in a file; every PG variable the device's login carries is cleared first, so its own PGSERVICE or PGHOST cannot redirect the dump; a pinned CA is written to a private directory removed however the run ends. pg_dump runs under timeout -k, so a hung one is stopped on the device. Returns the archive's size and SHA-256, and records its removal as the undo. A check makes every refusal a run makes and writes nothing. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.

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
| `dest` | `string` | yes | - | The archive's absolute path on the device, which must not exist, in a directory that does. |
| `schema_only` | `bool` | no | `false` | Dump the schema without the data. |
| `timeout` | `string` | no | `1h` | How long pg_dump may run, up to 24h, after which it is stopped on the device. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | The archive's path. |
| `size` | `int` | after a run | Its size in bytes. |
| `sha256` | `string` | after a run | The SHA-256 of the archive in hex, computed on the device. |
| `server_version` | `int` | always | The server's major version. |
| `tool_version` | `string` | always | pg_dump --version on the device. |
| `warnings` | `list of string` | when there is one | What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo removes the archive it created; it never replaces one, so nothing else is lost.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `db.pg.restore`
- `file.remove`

## Examples

Back a database up:

```yaml
- name: Nightly dump
  db.pg.dump:
    connection: appdb
    database: app
    dest: /var/backups/app-{{ vars.stamp }}.dump
```


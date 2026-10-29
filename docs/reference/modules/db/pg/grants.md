---
status: beta
---

# db.pg.grants

Compares the privileges granted to a role with a declared set, and fails naming each extra and each missing one.

Reads, in one snapshot, every privilege granted to role itself in the database (on the database, schemas, tables, views, sequences, functions and columns, outside the system schemas, each with whether it carries the grant option), the roles it is a member of, and the objects it owns, and compares them with expect. A pattern schema.* stands for every object of its kind in the schema that exists; an exact object that does not exist is missing. Privileges reached through PUBLIC or through another role's membership are that grant's to answer for and are not compared; an owner holds everything on what it owns, so ownership is compared as its own entry, extra unless owns declares it. A column grant is its own entry, never folded into its table's. A grant option held but not declared is extra, and one declared but not held is missing. Default privileges are not compared. A role or a database that does not exist holds nothing and is reported as exists false, in a run and in a check: the declared set then lacks the role itself, which missing names first with everything else it declares, so the task fails naming them rather than passing a set nothing could hold. It only reads, so a check of it is the task itself. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.

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
| `role` | `string` | yes | - | The role whose privileges are compared. |
| `expect` | `dict` | yes | - | The declared set: database (a list of privileges), schemas (schema to privileges), tables, sequences and functions (schema.name, or schema.* for every one in the schema, to privileges; a function as schema.name(argument types) the way pg_get_function_identity_arguments writes them), columns (schema.table.column to privileges), member_of (role names) and owns (what the role may own: database, a schema, schema.name, or schema.* for anything in it). A privilege is its SQL name (SELECT, USAGE, EXECUTE, ...), ALL for every one its kind has on this server (before PostgreSQL 17, a table has no MAINTAIN, and declaring it there is refused), and may end WITH GRANT OPTION. |
| `timeout` | `string` | no | `30s` | The timeout of each catalog read. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `extra` | `list of string` | on success | Every privilege, membership and owned object the role has that the set does not declare, which is empty for a task that succeeded. |
| `missing` | `list of string` | on success | Every one the set declares that the role lacks, which is empty for a task that succeeded. |
| `exists` | `bool` | always | Whether the role and the database exist; false when either does not, which missing names. |
| `warnings` | `list of string` | when there is one | What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the catalogs, and writes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `db.pg.query`

## Examples

Hold a reporting role to read-only:

```yaml
- name: Reporting role is read-only
  db.pg.grants:
    connection: appdb
    database: app
    role: reporting
    expect:
      database: [CONNECT]
      schemas: {public: [USAGE]}
      tables: {"public.*": [SELECT]}
```


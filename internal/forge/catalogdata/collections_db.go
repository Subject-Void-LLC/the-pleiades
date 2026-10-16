// This file holds the db section of the catalog: db.pg.query, db.pg.dump,
// db.pg.restore, db.pg.migrate, db.pg.grants and db.pg.rowcounts,
// PostgreSQL work a run does against a database it reaches from a device.
// The Doc literals are copies of internal/catalog/db/pg's docs.go, kept
// equal by internal/archtest's TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var dbCollections = []collectionscaffold.Config{
	{
		Name:          "db.pg.query",
		Capabilities:  []capability.Name{capability.NameSSHTransport},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one SQL statement and returns its rows as CSV, read-only unless the task says otherwise.",
			Description: "Prepares exactly one statement (the server refuses more than one in a prepared statement) and runs it in a transaction with a statement timeout and a lock timeout set for that transaction alone. Without write, the transaction is read-only and rolled back; with write, it is committed, and a data change without RETURNING is run for the count of rows it changed. The rows are returned as RFC 4180 CSV with a header row, NULL as an empty field and the empty string as \"\"; more than max_rows rows, or more than 1 MiB of CSV, fails the task rather than return part. A read-only transaction stops writes to the database's tables but is not a sandbox: a superuser, or a member of pg_execute_server_program, pg_read_server_files or pg_write_server_files, can still act outside the database (COPY TO PROGRAM, server files), so such a login is refused unless allow_privileged_role is set, which the run warns about. Give queries a role of their own with only the privileges they need. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.",
			Params: []collection.Param{
				{Name: "connection", Type: "string", Required: true, Description: "The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them."},
				{Name: "database", Type: "string", Required: true, Description: "The database to connect to."},
				{Name: "sql", Type: "string", Required: true, Description: "One SQL statement, with $1, $2, ... for args."},
				{Name: "args", Type: "list", Description: "Values for $1, $2, ..., sent as parameters, never written into the SQL."},
				{Name: "write", Type: "bool", Default: "false", Description: "Run in a read-write transaction and commit it. A task with write cannot be checked (check_mode is refused at validation)."},
				{Name: "timeout", Type: "string", Default: "30s", Description: "The statement timeout, as a duration such as 30s or 5m, up to 1h."},
				{Name: "lock_timeout", Type: "string", Default: "5s", Description: "How long the statement may wait for a lock, up to 10m: a read queued behind a schema change would otherwise hold up everything queued behind it."},
				{Name: "max_rows", Type: "int", Default: "1000", Description: "The most rows the task returns, up to 100000; past it the task fails. Use LIMIT in the SQL to ask for fewer."},
				{Name: "allow_privileged_role", Type: "bool", Default: "false", Description: "Allow a login that a read-only transaction does not confine (a superuser, or a member of pg_execute_server_program, pg_read_server_files or pg_write_server_files). The run warns about it."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "columns", Type: "list of string", Returned: "when the statement returns rows", Description: "The column names, in order."},
				{Name: "row_count", Type: "int", Returned: "when the statement returns rows", Description: "How many rows it returned."},
				{Name: "csv", Type: "string", Returned: "when the statement returns rows", Description: "The rows as CSV with a header row."},
				{Name: "rows_affected", Type: "int", Returned: "for a write without RETURNING", Description: "How many rows it changed."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch."},
			},
			Examples: []collection.Example{
				{Name: "Count a table's recent rows", RunbookYAML: "- name: Orders today\n  db.pg.query:\n    connection: appdb\n    database: app\n    sql: SELECT count(*) AS orders FROM orders WHERE created_at > now() - interval '1 day'\n  register: today\n"},
				{Name: "Look one row up by a value", RunbookYAML: "- name: Find the account\n  db.pg.query:\n    connection: appdb\n    database: app\n    sql: SELECT id, status FROM accounts WHERE email = $1\n    args: [\"{{ vars.email }}\"]\n"},
			},
			SeeAlso: []string{"db.pg.rowcounts", "db.pg.grants"},
		},
	},
	{
		Name:          "db.pg.dump",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Dumps a database with the device's pg_dump into a new archive on the device.",
			Description: "Runs pg_dump on the device in its custom format into dest, which must not exist: a dump never replaces an archive. The archive is written beside dest and linked to it once complete, so a failed or interrupted dump never leaves a partial archive under the name a restore would read. pg_dump must be at least the server's major version, which is checked before anything is written. The password reaches pg_dump on standard input, read by the shell into PGPASSWORD, in the environment of the script that runs it and nothing else, never on a command line or in a file; every PG variable the device's login carries is cleared first, so its own PGSERVICE or PGHOST cannot redirect the dump; a pinned CA is written to a private directory removed however the run ends. pg_dump runs under timeout -k, so a hung one is stopped on the device. Its removal is recorded as the undo as soon as the archive is in place, then its size and SHA-256 are read back and returned; a read back that fails is a warning, not a failure that would drop the undo. The database reaches pg_dump as a connection string naming it alone, quoted, so a name holding = is only ever a name. A check makes every refusal a run makes and writes nothing. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.",
			Params: []collection.Param{
				{Name: "connection", Type: "string", Required: true, Description: "The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them."},
				{Name: "database", Type: "string", Required: true, Description: "The database to connect to."},
				{Name: "dest", Type: "string", Required: true, Description: "The archive's absolute path on the device, which must not exist, in a directory that does."},
				{Name: "schema_only", Type: "bool", Default: "false", Description: "Dump the schema without the data."},
				{Name: "timeout", Type: "string", Default: "1h", Description: "How long pg_dump may run, up to 24h, after which it is stopped on the device."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "dest", Type: "string", Returned: "always", Description: "The archive's path."},
				{Name: "size", Type: "int", Returned: "after a run", Description: "Its size in bytes."},
				{Name: "sha256", Type: "string", Returned: "after a run", Description: "The SHA-256 of the archive in hex, computed on the device."},
				{Name: "server_version", Type: "int", Returned: "always", Description: "The server's major version."},
				{Name: "tool_version", Type: "string", Returned: "always", Description: "pg_dump --version on the device."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch."},
			},
			Examples: []collection.Example{
				{Name: "Back a database up", RunbookYAML: "- name: Nightly dump\n  db.pg.dump:\n    connection: appdb\n    database: app\n    dest: /var/backups/app-{{ vars.stamp }}.dump\n"},
			},
			SeeAlso: []string{"db.pg.restore", "file.remove"},
		},
	},
	{
		Name:          "db.pg.restore",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Restores an archive on the device into a database the task names, in one transaction, refusing a live one unless told why.",
			Description: "Runs the device's pg_restore with --single-transaction and --exit-on-error, so a restore that fails leaves nothing of itself behind. The target database is named by the task, never defaulted. A database is live when its connection lists it, or when it says so itself (ALTER DATABASE app SET pleiades.live = on): restoring over one is refused unless allow_live is set with a live_reason, which the result records and the run warns about. A database that does not exist is refused unless create is set, in which case it is created first and dropped again if the restore fails; one that already holds tables, views or sequences is refused unless clean is set (--clean --if-exists). By default objects are restored owned by the connection's login and without the archive's grants (no_owner, no_privileges), which suits a copy restored for verification; turn both off to restore a database as it was. The archive is listed (pg_restore --list) before anything changes, which refuses a file that is not one. The database reaches pg_restore as a connection string naming it alone, quoted, so a name holding = is only ever a name and never another server. A check makes every refusal a run makes and writes nothing. It cannot be undone: dump the target first to keep a way back. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.",
			Params: []collection.Param{
				{Name: "connection", Type: "string", Required: true, Description: "The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them."},
				{Name: "database", Type: "string", Required: true, Description: "The database to restore into, named explicitly."},
				{Name: "src", Type: "string", Required: true, Description: "The archive's absolute path on the device, as db.pg.dump writes it."},
				{Name: "create", Type: "bool", Default: "false", Description: "Create the database when it does not exist (dropped again if the restore fails)."},
				{Name: "clean", Type: "bool", Default: "false", Description: "Restore into a database that already holds objects, dropping each the archive restores first."},
				{Name: "allow_live", Type: "bool", Default: "false", Description: "Allow restoring over a live database. Needs live_reason."},
				{Name: "live_reason", Type: "string", Description: "Why a live database is being overwritten, recorded in the result and warned about."},
				{Name: "no_owner", Type: "bool", Default: "true", Description: "Restore objects owned by the connection's login rather than the archive's owners (--no-owner)."},
				{Name: "no_privileges", Type: "bool", Default: "true", Description: "Leave out the archive's grants (--no-privileges)."},
				{Name: "maintenance_database", Type: "string", Default: "postgres", Description: "The database connected to while the target is checked, created or dropped."},
				{Name: "timeout", Type: "string", Default: "1h", Description: "How long pg_restore may run, up to 24h, after which it is stopped on the device and its transaction rolled back."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "database", Type: "string", Returned: "always", Description: "The database restored into."},
				{Name: "created", Type: "bool", Returned: "always", Description: "Whether it was created (or, in a check, would be)."},
				{Name: "toc_entries", Type: "int", Returned: "always", Description: "How many entries the archive lists."},
				{Name: "live_override", Type: "string", Returned: "when a live database was overwritten", Description: "The live_reason the task gave."},
				{Name: "server_version", Type: "int", Returned: "always", Description: "The server's major version."},
				{Name: "tool_version", Type: "string", Returned: "always", Description: "pg_restore --version on the device."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch."},
			},
			Examples: []collection.Example{
				{Name: "Restore last night's dump into a scratch database", RunbookYAML: "- name: Restore to scratch\n  db.pg.restore:\n    connection: appdb\n    database: app_restore_check\n    src: /var/backups/app.dump\n    create: true\n"},
			},
			SeeAlso: []string{"db.pg.dump", "db.pg.rowcounts"},
		},
	},
	{
		Name:          "db.pg.migrate",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Applies a directory of numbered SQL files in order, each in its own transaction, recording each in a version table.",
			Description: "Reads every .sql file in dir on the device, named <version>_<name>.sql and applied in numeric order. Another .sql name, in any letter case (1_x.SQL), is refused rather than skipped, as is a .down.sql file (this applies forward only) and a version two files share. Every file is read and checked before any runs: a file may not commit or roll back its own transaction, and a statement that cannot run in a transaction (CREATE INDEX CONCURRENTLY, VACUUM) needs a file of its own starting -- pleiades: no-transaction, holding exactly that one statement. With additive_only, a file that drops, truncates, deletes, revokes, renames or changes a column's type is refused unless it says why (-- pleiades: destructive: <why>), which the result lists, and so is one whose effect the lint cannot read: a DO block, a CALL, an EXECUTE, a COPY to or from a program, or a query on its own (SELECT f() does whatever f does); a statement that runs another (WITH, PREPARE, EXPLAIN, COPY of a query) is judged by the one it runs. The check is a SQL lexer, so a keyword in a string or comment does not count, and it does not see into a function or trigger that an INSERT, UPDATE or CREATE sets off. Each file runs in its own transaction, as role when given, with a statement timeout and a lock timeout (a busy table fails the migration rather than stalling the application), and its version, name, SHA-256 and duration are recorded in the version table in the same transaction. An applied file whose checksum changed is refused, as is a pending version below the newest applied one unless allow_out_of_order is set. Runs against one version table are serialized by the version itself, with no lock: each file claims its version as the table's primary key before it runs, so a second run applying the same file waits on the first's claim and then reads its record rather than applying it again; a no-transaction file's claim is recorded first as unfinished and marked finished once its statement succeeds (removed if it fails), so a run that died midway is named rather than guessed at. The lint reads a backslash in a string literal as the standard does, so before each file is sent the session must have standard_conforming_strings on, and a file that turns it off stops the run before the next. A check reads and checks every file, reads the version table and reports the pending files, applying nothing. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.",
			Params: []collection.Param{
				{Name: "connection", Type: "string", Required: true, Description: "The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them."},
				{Name: "database", Type: "string", Required: true, Description: "The database to connect to."},
				{Name: "dir", Type: "string", Required: true, Description: "The directory of migration files on the device."},
				{Name: "table", Type: "string", Default: "public.pleiades_migrations", Description: "The version table, as schema.name; created when missing, owned by role when given."},
				{Name: "role", Type: "string", Description: "The role each file runs as (SET ROLE), so it owns what the file creates. The connection's login must be a member."},
				{Name: "additive_only", Type: "bool", Default: "false", Description: "Refuse a destructive statement in a file that does not say why."},
				{Name: "allow_out_of_order", Type: "bool", Default: "false", Description: "Apply a pending version below the newest applied one."},
				{Name: "up_to", Type: "int", Description: "Apply pending versions up to this one only; 0 is a version, so it applies nothing above version 0."},
				{Name: "timeout", Type: "string", Default: "15m", Description: "Each file's statement timeout, up to 24h."},
				{Name: "lock_timeout", Type: "string", Default: "5s", Description: "How long a statement may wait for a lock, up to 10m."},
				{Name: "claim_wait", Type: "string", Default: "15m", Description: "How long to wait for another run applying the same version to finish, up to 24h."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "applied", Type: "list of dict", Returned: "after a run", Description: "Each file applied: version, name and duration_ms."},
				{Name: "pending", Type: "list of dict", Returned: "always", Description: "Each file still pending (in a check, the files a run would apply): version and name."},
				{Name: "destructive", Type: "list of dict", Returned: "always", Description: "Each file that says it is destructive: file and reason."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch."},
			},
			Examples: []collection.Example{
				{Name: "Apply the application's migrations", RunbookYAML: "- name: Migrate\n  db.pg.migrate:\n    connection: appdb\n    database: app\n    dir: /srv/app/migrations\n    role: app_owner\n    additive_only: true\n"},
			},
			SeeAlso: []string{"db.pg.query", "db.pg.grants"},
		},
	},
	{
		Name:          "db.pg.grants",
		Capabilities:  []capability.Name{capability.NameSSHTransport},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Compares the privileges granted to a role with a declared set, and fails naming each extra and each missing one.",
			Description: "Reads, in one snapshot, every privilege granted to role itself in the database (on the database, schemas, tables, views, sequences, functions and columns, outside the system schemas, each with whether it carries the grant option), the roles it is a member of, and the objects it owns, and compares them with expect. A pattern schema.* stands for every object of its kind in the schema that exists; an exact object that does not exist is missing. Privileges reached through PUBLIC or through another role's membership are that grant's to answer for and are not compared; an owner holds everything on what it owns, so ownership is compared as its own entry, extra unless owns declares it. A column grant is its own entry, never folded into its table's. A grant option held but not declared is extra, and one declared but not held is missing. Default privileges are not compared. It only reads, so a check of it is the task itself. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.",
			Params: []collection.Param{
				{Name: "connection", Type: "string", Required: true, Description: "The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them."},
				{Name: "database", Type: "string", Required: true, Description: "The database to connect to."},
				{Name: "role", Type: "string", Required: true, Description: "The role whose privileges are compared."},
				{Name: "expect", Type: "dict", Required: true, Description: "The declared set: database (a list of privileges), schemas (schema to privileges), tables, sequences and functions (schema.name, or schema.* for every one in the schema, to privileges; a function as schema.name(argument types) the way pg_get_function_identity_arguments writes them), columns (schema.table.column to privileges), member_of (role names) and owns (what the role may own: database, a schema, schema.name, or schema.* for anything in it). A privilege is its SQL name (SELECT, USAGE, EXECUTE, ...), ALL for every one its kind has on this server (before PostgreSQL 17, a table has no MAINTAIN, and declaring it there is refused), and may end WITH GRANT OPTION."},
				{Name: "timeout", Type: "string", Default: "30s", Description: "The timeout of each catalog read."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "extra", Type: "list of string", Returned: "on success", Description: "Every privilege, membership and owned object the role has that the set does not declare, which is empty for a task that succeeded."},
				{Name: "missing", Type: "list of string", Returned: "on success", Description: "Every one the set declares that the role lacks, which is empty for a task that succeeded."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch."},
			},
			Examples: []collection.Example{
				{Name: "Hold a reporting role to read-only", RunbookYAML: "- name: Reporting role is read-only\n  db.pg.grants:\n    connection: appdb\n    database: app\n    role: reporting\n    expect:\n      database: [CONNECT]\n      schemas: {public: [USAGE]}\n      tables: {\"public.*\": [SELECT]}\n"},
			},
			SeeAlso: []string{"db.pg.query"},
		},
	},
	{
		Name:          "db.pg.rowcounts",
		Capabilities:  []capability.Name{capability.NameSSHTransport},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Counts every table's rows exactly, and with compare fails naming each table whose counts differ from a second database's.",
			Description: "Counts the rows of every table outside the system schemas with count(*), in a REPEATABLE READ READ ONLY transaction per database, so each side's counts are one moment; partitions are counted through their parent, once. With compare, a second database (on the same connection, or another named one, reached through the same device) is counted the same way and the two are compared table by table: a different count, or a table only one side has, fails the task. Comparing a live database with a copy of it differs by whatever was written since; the comparison is for copies at rest, such as a restore checked against the dump's source taken at the same time. Planner estimates are never used. It only reads, so a check of it is the task itself. The connection is made from the device, through its SSH session (a direct-tcpip channel, or direct-streamlocal to a socket), so the database sees the connection its own network and pg_hba.conf already allow; TLS, when the connection asks for it, is negotiated and verified by this platform against the connection's pinned CA or the system's roots, and nothing of the machine running pleiades (a .pgpass, a certificate, a PG variable) is read.",
			Params: []collection.Param{
				{Name: "connection", Type: "string", Required: true, Description: "The name of a database connection: one stored with pleiades add-database-connection, or a PostgreSQL Connection credential bound to the template. It holds the server as the device sees it, the login and its password, the TLS mode and the live databases; a task never carries any of them."},
				{Name: "database", Type: "string", Required: true, Description: "The database to connect to."},
				{Name: "compare", Type: "dict", Description: "The database to compare with: database, and connection when it is on another server. Any other key is refused."},
				{Name: "schemas", Type: "list of string", Description: "Count only the tables in these schemas, each of which must exist."},
				{Name: "exclude", Type: "list of string", Description: "Tables not to count, as schema.table or schema.*."},
				{Name: "max_tables", Type: "int", Default: "1000", Description: "The most tables one side may count, up to 10000; past it the task fails rather than count part."},
				{Name: "timeout", Type: "string", Default: "5m", Description: "Each count's statement timeout, up to 1h."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "tables", Type: "list of dict", Returned: "on success", Description: "Each table counted: table, rows, and compare_rows with compare."},
				{Name: "differences", Type: "int", Returned: "on success", Description: "How many tables' counts differ, which is zero for a task that succeeded."},
				{Name: "only_here", Type: "list of string", Returned: "on success", Description: "Tables only the first database has."},
				{Name: "only_there", Type: "list of string", Returned: "on success", Description: "Tables only the compared database has."},
				{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the run allowed that it must say: a connection that crosses a network unencrypted, a privileged login, a live database overwritten, a version mismatch."},
			},
			Examples: []collection.Example{
				{Name: "Check a restored copy against its source", RunbookYAML: "- name: Restore matches\n  db.pg.rowcounts:\n    connection: appdb\n    database: app_restore_check\n    compare:\n      database: app\n"},
			},
			SeeAlso: []string{"db.pg.restore", "db.pg.query"},
		},
	},
}

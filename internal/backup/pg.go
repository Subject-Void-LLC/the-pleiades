// The PostgreSQL database a DB_DSN names, and PostgreSQL's own client
// programs run against it.
package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrNotPostgres is returned for a DB_DSN that does not name a PostgreSQL
// database. A SQLite deployment is backed up by copying its file while the
// controller is stopped, with the key kept apart from the copy.
var ErrNotPostgres = errors.New("backup: DB_DSN does not name a PostgreSQL database; backup and restore work on the compose stack's PostgreSQL")

// identPattern bounds a database or role name this command derives others
// from. Every name it creates is the database's name plus a suffix, so a
// bounded plain identifier keeps each one under PostgreSQL's 63 byte limit
// and needs no quoting beyond the double quotes it always gets.
var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,40}$`)

// passedParams are the connection settings a DB_DSN may carry through to
// the client programs. Anything else is refused rather than dropped, since a
// setting silently dropped is a connection that differs from the
// controller's.
var passedParams = map[string]bool{
	"sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true, "connect_timeout": true,
}

// target is one database on the server a DB_DSN names.
type target struct {
	host, port, user, password, database string
	params                               url.Values
}

// parseTarget reads a postgres:// DSN, the form docker-compose.yml's DB_DSN
// takes.
func parseTarget(dsn string) (target, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return target{}, ErrNotPostgres
	}
	t := target{host: u.Hostname(), port: u.Port(), user: u.User.Username(), params: url.Values{}}
	t.password, _ = u.User.Password()
	t.database = strings.TrimPrefix(u.Path, "/")
	if t.port == "" {
		t.port = "5432"
	}
	if t.host == "" {
		return target{}, errors.New("backup: DB_DSN names no host")
	}
	if !identPattern.MatchString(t.database) || !identPattern.MatchString(t.user) {
		return target{}, errors.New("backup: DB_DSN's database and user must be lowercase letters, digits and underscores, at most 41 characters")
	}
	for name, values := range u.Query() {
		if !passedParams[name] {
			return target{}, fmt.Errorf("backup: DB_DSN sets %s, which backup and restore do not pass on", printable(name))
		}
		t.params[name] = values
	}
	return t, nil
}

// on returns the same server with another database and login.
func (t target) on(database, user, password string) target {
	t.database, t.user, t.password = database, user, password
	return t
}

// dsn is the URL form, with the password, for this process's own database
// connections. It never leaves the process.
func (t target) dsn() string {
	u := url.URL{
		Scheme: "postgres", User: url.UserPassword(t.user, t.password),
		Host: t.host + ":" + t.port, Path: "/" + t.database, RawQuery: t.params.Encode(),
	}
	return u.String()
}

// conninfo is libpq's keyword form WITHOUT the password, for a client
// program's argument list, which every user on the machine can read.
func (t target) conninfo() string {
	parts := []string{
		"host=" + quoteConninfo(t.host), "port=" + quoteConninfo(t.port),
		"user=" + quoteConninfo(t.user), "dbname=" + quoteConninfo(t.database),
		"application_name=pleiades-backup",
	}
	names := make([]string, 0, len(t.params))
	for name := range t.params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, name+"="+quoteConninfo(t.params.Get(name)))
	}
	return strings.Join(parts, " ")
}

// cloneValues copies v, so a change to the copy leaves v alone.
func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// quoteConninfo quotes a conninfo value the way libpq reads one: single
// quotes, with a backslash before any quote or backslash inside.
func quoteConninfo(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

// quoteIdent quotes an identifier for SQL. Names reaching here have already
// matched identPattern; quoting is still applied, because an identifier
// built into a statement is exactly the place a later change that loosened
// the pattern would become an injection.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// tools runs pg_dump and pg_restore, which must be on PATH. The backup
// image carries the ones from the server's own PostgreSQL release.
type tools struct{}

// passFile writes a libpq password file for logins and returns the path of
// a private directory holding it, which the caller removes. The password
// file is how a password reaches a client program without appearing in its
// arguments, which anyone can read, or its environment, which is inherited.
func passFile(logins ...target) (string, error) {
	dir, err := os.MkdirTemp("", "pleiades-pgpass-")
	if err != nil {
		return "", fmt.Errorf("backup: creating a private directory: %w", err)
	}
	escape := strings.NewReplacer(`\`, `\\`, `:`, `\:`)
	var b strings.Builder
	for _, t := range logins {
		if t.user == "" {
			continue
		}
		b.WriteString("*:*:*:" + escape.Replace(t.user) + ":" + escape.Replace(t.password) + "\n")
	}
	// libpq ignores a password file anyone else can read, so 0600 is also
	// what makes it work.
	if err := os.WriteFile(filepath.Join(dir, "pgpass"), []byte(b.String()), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("backup: writing the password file: %w", err)
	}
	return dir, nil
}

// run runs one client program as login, with stdin and stdout as given. Its
// environment is built from nothing, so neither DB_DSN nor any other
// setting of this process reaches it. A zero login is a program that
// connects to nothing.
func (tools) run(ctx context.Context, login target, program string, args []string, stdin io.Reader, stdout io.Writer) error {
	dir, err := passFile(login)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// #nosec G204 -- program is one of two constant names, and args are
	// built by this package: flags, a conninfo without the password, and
	// nothing an operator typed.
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"PGPASSFILE=" + filepath.Join(dir, "pgpass"),
		"HOME=" + dir,
		"LC_ALL=C",
	}
	cmd.Stdin, cmd.Stdout = stdin, stdout
	var stderr bytes.Buffer
	cmd.Stderr = &limitedBuffer{buf: &stderr, max: 64 << 10}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed (%v)%s", program, err, toolReason(stderr.String()))
	}
	return nil
}

// dump writes a custom-format archive of login's database to out.
func (tl tools) dump(ctx context.Context, login target, out io.Writer) error {
	return tl.run(ctx, login, "pg_dump", []string{
		"--format=custom", "--no-password", "--dbname=" + login.conninfo(),
	}, nil, out)
}

// list prints archive's table of contents. archive is read from standard
// input, so the file is opened by this process, inside its confined
// directory, rather than by a path pg_restore resolves itself.
func (tl tools) list(ctx context.Context, archive io.Reader) ([]byte, error) {
	var out bytes.Buffer
	err := tl.run(ctx, target{}, "pg_restore", []string{"--list"}, archive, &limitedBuffer{buf: &out, max: maxTOCBytes + 1})
	return out.Bytes(), err
}

// restore loads archive into login's database in one transaction, owned by
// login and granting nothing, stopping at the first error.
func (tl tools) restore(ctx context.Context, login target, archive io.Reader) error {
	return tl.run(ctx, login, "pg_restore", []string{
		"--no-password", "--dbname=" + login.conninfo(),
		"--single-transaction", "--exit-on-error",
		"--no-owner", "--no-privileges", "--no-tablespaces", "--no-comments",
		"--no-publications", "--no-subscriptions", "--no-security-labels",
	}, archive, io.Discard)
}

// quoted matches a double-quoted span in a tool's error text.
var quoted = regexp.MustCompile(`"[^"\n]*"`)

// toolReason keeps the lines of a client program's error output that say
// why it failed, with every double-quoted span hidden. PostgreSQL quotes the
// value it could not accept ("invalid input syntax for type integer: ..."),
// and a value from a backup is data that must not reach an error message.
func toolReason(stderr string) string {
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.Contains(line, "error:") && !strings.Contains(line, "FATAL:") {
			continue
		}
		kept = append(kept, printableUpTo(quoted.ReplaceAllString(line, `"(value hidden)"`), 240))
		if len(kept) == 3 {
			break
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return ": " + strings.Join(kept, "; ")
}

// limitedBuffer keeps the first max bytes written to it and discards the
// rest, still reporting every write as complete so the writer carries on.
type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

// Write implements io.Writer.
func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

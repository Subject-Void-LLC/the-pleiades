// The add-database-connection, list-database-connections and
// remove-database-connection commands: named PostgreSQL connections a
// db.pg task refers to, kept apart from every device's credential.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// maxCAFileBytes bounds a CA file read for a connection.
const maxCAFileBytes = 1 << 20

// runAddDatabaseConnection stores the connection named by its positional
// argument. A run gives it only to a db.pg method whose task names it
// (sdk.DatabaseConnections), so its password reaches no device and no
// other method. It lives in the credential file's own databases section,
// which no device name can address.
func runAddDatabaseConnection(args []string) error {
	boolFlags := map[string]bool{"password-stdin": true, "no-password": true, "insecure-transport": true}
	name, rest, err := splitPositional(args, boolFlags)
	if err != nil {
		return fmt.Errorf("usage: pleiades add-database-connection <name> --host <host> --user <user> [--password-stdin | --no-password]: %w", err)
	}
	fs := flag.NewFlagSet("add-database-connection", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	host := fs.String("host", "", "the server as the device a task runs on sees it: a name, an address, or its Unix socket directory")
	port := fs.Int("port", 0, "the server's port (default 5432)")
	user := fs.String("user", "", "the login")
	stdinPassword := fs.Bool("password-stdin", false, "read the password as one line on standard input")
	noPassword := fs.Bool("no-password", false, "send no password (peer or trust authentication)")
	sslmode := fs.String("sslmode", wire.SSLVerifyFull, "verify-full, or disable for a local server")
	caFile := fs.String("ca-file", "", "a PEM file of the authority the server's certificate must chain to (default: the system's roots)")
	serverName := fs.String("server-name", "", "the name the server's certificate is checked against, when it is not --host")
	insecure := fs.Bool("insecure-transport", false, "allow --sslmode disable to a server that is not local: the password and every row cross that network in the clear")
	live := fs.String("live", "", "comma-separated databases on this server a restore refuses unless the task overrides it")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *stdinPassword && *noPassword {
		return fmt.Errorf("--password-stdin and --no-password cannot both be given")
	}
	conn := wire.DatabaseConnection{
		Name: name, Host: *host, Port: *port, User: *user, SSLMode: *sslmode,
		ServerName: *serverName, InsecureTransport: *insecure,
	}
	for _, db := range strings.Split(*live, ",") {
		if db = strings.TrimSpace(db); db != "" {
			conn.Live = append(conn.Live, db)
		}
	}
	if *caFile != "" {
		data, err := readBounded(*caFile, maxCAFileBytes)
		if err != nil {
			return err
		}
		conn.CAPEM = string(data)
	}
	// Refused before a password is asked for, so a typo costs no prompt.
	if err := conn.Validate(); err != nil {
		return err
	}
	switch {
	case *noPassword:
	case *stdinPassword:
		conn.Password, err = prompt.SecretFromStdin()
	default:
		conn.Password, err = promptSecret("password: ")
	}
	if err != nil {
		return err
	}
	if conn.Password == "" && !*noPassword {
		return fmt.Errorf("the password is empty; pass --no-password for a login that sends none")
	}
	key, err := credential.ResolveMasterKey(*dir)
	if err != nil {
		return fmt.Errorf("failed to resolve credential master key: %w", err)
	}
	if err := credential.SaveDatabaseConnection(*dir, key, conn); err != nil {
		return err
	}
	fmt.Printf("stored database connection %s (%s@%s:%d)\n", conn.Name, termsafe.EscapeLine(conn.User), termsafe.EscapeLine(conn.Host), conn.EffectivePort())
	if conn.Plaintext() {
		fmt.Fprintf(os.Stderr, "warning: connection %s sends its password and every row to %s in the clear\n", conn.Name, termsafe.EscapeLine(conn.Host))
	}
	return nil
}

// databaseConnectionView is one connection as list-database-connections
// shows it: everything but the password, and whether there is one.
type databaseConnectionView struct {
	Name              string   `json:"name"`
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	User              string   `json:"user"`
	SSLMode           string   `json:"sslmode"`
	PinnedCA          bool     `json:"pinned_ca"`
	ServerName        string   `json:"server_name,omitempty"`
	InsecureTransport bool     `json:"insecure_transport"`
	Live              []string `json:"live"`
}

// runListDatabaseConnections lists the stored connections, never a
// password.
func runListDatabaseConnections(args []string) error {
	fs := flag.NewFlagSet("list-database-connections", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: pleiades list-database-connections [--dir DIR] [--json]")
	}
	conns, err := credential.ListDatabaseConnections(*dir)
	if err != nil {
		return err
	}
	views := make([]databaseConnectionView, len(conns))
	for i, c := range conns {
		views[i] = databaseConnectionView{
			Name: c.Name, Host: c.Host, Port: c.EffectivePort(), User: c.User, SSLMode: c.EffectiveSSLMode(),
			PinnedCA: c.CAPEM != "", ServerName: c.ServerName, InsecureTransport: c.InsecureTransport, Live: append([]string{}, c.Live...),
		}
	}
	if *asJSON {
		return writeJSON(os.Stdout, views)
	}
	if len(views) == 0 {
		fmt.Println("no database connections stored in this project")
		return nil
	}
	for _, v := range views {
		notes := []string{v.SSLMode}
		if v.PinnedCA {
			notes = append(notes, "pinned CA")
		}
		if v.InsecureTransport {
			notes = append(notes, "INSECURE TRANSPORT")
		}
		if len(v.Live) > 0 {
			notes = append(notes, "live: "+strings.Join(v.Live, ", "))
		}
		fmt.Printf("%s  %s@%s:%d  %s\n", v.Name, termsafe.EscapeLine(v.User), termsafe.EscapeLine(v.Host), v.Port, termsafe.EscapeLine(strings.Join(notes, "; ")))
	}
	return nil
}

// runRemoveDatabaseConnection removes the named connection.
func runRemoveDatabaseConnection(args []string) error {
	name, rest, err := splitPositional(args, nil)
	if err != nil || name == "" {
		return fmt.Errorf("usage: pleiades remove-database-connection <name> [--dir DIR]")
	}
	fs := flag.NewFlagSet("remove-database-connection", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	removed, err := credential.RemoveDatabaseConnection(*dir, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no database connection named %s", termsafe.EscapeLine(name))
	}
	fmt.Printf("removed database connection %s\n", name)
	return nil
}

// readBounded reads a file of at most max bytes, refusing a longer one.
func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a path the operator names on the command line
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is over %d bytes", path, max)
	}
	return data, nil
}

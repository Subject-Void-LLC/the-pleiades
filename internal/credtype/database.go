// The database connection credential: a named PostgreSQL connection a
// db.pg task refers to, carried to the run as a value, never an
// environment variable or a file.
package credtype

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The input ids a database connection type declares
// (pleiades_postgres.json), each a field of wire.DatabaseConnection.
const (
	DatabaseInputName              = "name"
	DatabaseInputHost              = "host"
	DatabaseInputPort              = "port"
	DatabaseInputUsername          = "username"
	DatabaseInputPassword          = "password"
	DatabaseInputSSLMode           = "sslmode"
	DatabaseInputCAPEM             = "ca_pem"
	DatabaseInputServerName        = "server_name"
	DatabaseInputInsecureTransport = "insecure_transport"
	DatabaseInputLive              = "live"
)

// DatabaseNameOf extracts a database connection's name from a credential's
// input values, and returns "" for any other kind.
func DatabaseNameOf(kind Kind, inputs map[string]string) string {
	if kind != KindDatabase {
		return ""
	}
	return strings.TrimSpace(inputs[DatabaseInputName])
}

// databaseInputOf names the input a connection's field is read from: the
// same name, but for the login, which the type calls username.
func databaseInputOf(field string) string {
	if field == "user" {
		return DatabaseInputUsername
	}
	return field
}

// databaseTarget turns a database connection credential into one named
// wire.DatabaseConnection on the artifact. Like registryTarget it is code:
// its output is a value a Collection method asks its run for by name
// (sdk.DatabaseConnections), so nothing but the method a task points at it
// ever holds the password.
type databaseTarget struct{}

func (databaseTarget) Key() string  { return "database" }
func (databaseTarget) Phase() Phase { return PhaseValues }

// Apply maps a database kind's inputs onto one connection, refusing one
// no run could use under the same rules a stored connection is held to.
func (databaseTarget) Apply(req Request, art *Artifact) error {
	cred := req.Credential
	if cred.Type.Kind != KindDatabase {
		return nil
	}
	in := func(id string) string { return strings.TrimSpace(cred.Inputs[id]) }
	conn := wire.DatabaseConnection{
		Name: in(DatabaseInputName), Host: in(DatabaseInputHost), User: in(DatabaseInputUsername),
		Password: cred.Inputs[DatabaseInputPassword], SSLMode: in(DatabaseInputSSLMode),
		CAPEM: cred.Inputs[DatabaseInputCAPEM], ServerName: in(DatabaseInputServerName),
		InsecureTransport: in(DatabaseInputInsecureTransport) == "true",
	}
	// Every refusal below names the input and the rule it breaks, never the
	// value (TestNoInjectionRefusalQuotesAnInputValue): a custom type may mark
	// any of these inputs secret, and the refusal reaches a job's failure
	// reason, where nothing masks it.
	if p := in(DatabaseInputPort); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("%w: credential %q: input %q is not a number", ErrInjection, cred.Name, DatabaseInputPort)
		}
		conn.Port = port
	}
	for _, db := range strings.Split(cred.Inputs[DatabaseInputLive], ",") {
		if db = strings.TrimSpace(db); db != "" {
			conn.Live = append(conn.Live, db)
		}
	}
	if problem := conn.Problem(); problem != nil {
		return fmt.Errorf("%w: credential %q: input %q %s", ErrInjection, cred.Name, databaseInputOf(problem.Field), problem.Problem)
	}
	if _, dup := wire.FindDatabaseConnection(art.databases, conn.Name); dup {
		return fmt.Errorf("%w: credential %q: its %q input is another connection's too, and a task names a connection by it",
			ErrInjection, cred.Name, DatabaseInputName)
	}
	art.databases = append(art.databases, conn)
	return nil
}

// A named database connection on its way to the one method that may use
// it.
package wire

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/netip"
	"path"
	"regexp"
	"strings"
)

// DatabaseConnection is one PostgreSQL server as a run reaches it: where,
// as which login, how its transport is protected, and which of its
// databases are live. A task names it (connection: appdb) rather than
// carrying any of it, so the password is never in a runbook. It travels
// beside a dispatch's machine credential, like a registry credential, and
// a method gets it only by name (sdk.DatabaseConnections). External
// Collections never receive one.
type DatabaseConnection struct {
	// Name is what a task calls it.
	Name string `json:"name"`
	// Host is the server as the device a task runs on sees it: a name, an
	// address, or the absolute directory of its Unix socket.
	Host string `json:"host"`
	// Port is the server's port; 0 means 5432. With a socket directory it
	// names the socket (.s.PGSQL.<port>).
	Port int `json:"port,omitempty"`
	// User is the login, and Password its password; an empty password
	// sends none (peer or trust authentication).
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	// SSLMode is SSLVerifyFull (the default when empty) or SSLDisable.
	SSLMode string `json:"sslmode,omitempty"`
	// CAPEM pins the authority a server certificate must chain to; empty
	// means the system's roots.
	CAPEM string `json:"ca_pem,omitempty"`
	// ServerName is the name the certificate is checked against when it
	// is not Host (an address, or a name the certificate does not hold).
	ServerName string `json:"server_name,omitempty"`
	// InsecureTransport allows SSLDisable to a host that is not loopback
	// or a socket: the password and every row then cross that network in
	// the clear, so it is asked for by name and warned about on every run.
	InsecureTransport bool `json:"insecure_transport,omitempty"`
	// Live names the databases on this server a restore refuses unless the
	// task overrides it and says why.
	Live []string `json:"live,omitempty"`
}

// The transport protections a connection may ask for.
const (
	SSLVerifyFull = "verify-full"
	SSLDisable    = "disable"
)

// DefaultDatabasePort is PostgreSQL's port.
const DefaultDatabasePort = 5432

// databaseNamePattern is a connection's name, and databaseIdentPattern a
// database name a connection may list as live.
var (
	databaseNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	databaseIdentPattern = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,63}$`)
)

// ValidDatabaseConnectionName reports whether name can name a connection.
func ValidDatabaseConnectionName(name string) bool { return databaseNamePattern.MatchString(name) }

// EffectivePort is Port, or the default when it is 0.
func (c DatabaseConnection) EffectivePort() int {
	if c.Port == 0 {
		return DefaultDatabasePort
	}
	return c.Port
}

// EffectiveSSLMode is SSLMode, or SSLVerifyFull when it is empty.
func (c DatabaseConnection) EffectiveSSLMode() string {
	if c.SSLMode == "" {
		return SSLVerifyFull
	}
	return c.SSLMode
}

// Socket reports whether Host is a Unix socket directory.
func (c DatabaseConnection) Socket() bool { return path.IsAbs(c.Host) }

// Local reports whether the server is on the device itself: a socket, or
// a loopback name or address. The stream from the device to it never
// crosses a network, so it may go unencrypted (the device's own transport
// is encrypted).
func (c DatabaseConnection) Local() bool {
	if c.Socket() || strings.EqualFold(c.Host, "localhost") {
		return true
	}
	ip := net.ParseIP(c.Host)
	return ip != nil && ip.IsLoopback()
}

// Plaintext reports whether the connection crosses a network unencrypted,
// which only InsecureTransport allows.
func (c DatabaseConnection) Plaintext() bool {
	return c.EffectiveSSLMode() == SSLDisable && !c.Local()
}

// A ConnectionProblem is why a connection cannot be used: the field it is
// about, by its JSON name, and what is wrong with it. It never repeats a
// field's value. A connection bound to a job template is built from a
// credential's inputs, which a custom credential type may mark secret, and
// a refusal reaches an API response and a job's failure reason, where
// nothing masks it; a host written as a URL or as user:password@host
// carries a password even when it is not marked secret.
type ConnectionProblem struct {
	// Field is the field's JSON name: name, host, port, user, sslmode,
	// ca_pem, server_name, insecure_transport or live.
	Field string
	// Problem says what is wrong with it, as a clause following its name.
	Problem string
}

// Error reads as the field's name followed by its problem.
func (p *ConnectionProblem) Error() string { return p.Field + " " + p.Problem }

// problem builds a ConnectionProblem.
func problem(field, text string) *ConnectionProblem {
	return &ConnectionProblem{Field: field, Problem: text}
}

// Validate refuses a connection no run could use as written, with a
// *ConnectionProblem naming the field and never its value (Problem).
func (c DatabaseConnection) Validate() error {
	if p := c.Problem(); p != nil {
		return p
	}
	return nil
}

// Problem says why no run could use the connection as written, or nil when
// one can.
func (c DatabaseConnection) Problem() *ConnectionProblem {
	switch {
	case !ValidDatabaseConnectionName(c.Name):
		return problem("name", "is not a connection name: a lowercase letter, then up to 62 lowercase letters, digits, - and _")
	case hostProblem(c.Host) != "":
		return problem("host", hostProblem(c.Host))
	case c.Port < 0 || c.Port > 65535:
		return problem("port", "is not a port: 1 to 65535, or 0 for the default")
	case c.User == "" || strings.ContainsAny(c.User, "\x00\r\n"):
		return problem("user", "is empty or holds a NUL or a line break")
	}
	switch c.EffectiveSSLMode() {
	case SSLVerifyFull:
		if c.Socket() {
			return problem("sslmode", "is "+SSLVerifyFull+" for a socket, which carries no TLS; use "+SSLDisable)
		}
		if c.InsecureTransport {
			return problem("insecure_transport", "allows nothing with sslmode "+SSLVerifyFull+"; it is for "+SSLDisable+" to a host that is not local")
		}
	case SSLDisable:
		if c.CAPEM != "" {
			return problem("ca_pem", "means nothing with sslmode "+SSLDisable)
		}
		if c.ServerName != "" {
			return problem("server_name", "means nothing with sslmode "+SSLDisable)
		}
		if c.Plaintext() && !c.InsecureTransport {
			return problem("host", "is not local, and with sslmode "+SSLDisable+" the password and every row would cross the network in the clear; set insecure_transport to allow it, or use "+SSLVerifyFull)
		}
		if !c.Plaintext() && c.InsecureTransport {
			return problem("insecure_transport", "allows nothing for a local server")
		}
	default:
		return problem("sslmode", "is not "+SSLVerifyFull+" or "+SSLDisable)
	}
	if c.CAPEM != "" {
		if text := caProblem(c.CAPEM); text != "" {
			return problem("ca_pem", text)
		}
	}
	for _, db := range c.Live {
		if !databaseIdentPattern.MatchString(db) {
			return problem("live", "names a database that is not one: 1 to 63 characters, none a control character")
		}
	}
	return nil
}

// hostProblem says why host cannot be a connection's host, or "" when it
// can: a name, an address (an IPv6 one bare, with or without a zone), or
// the absolute directory of a Unix socket. It never repeats the host.
func hostProblem(host string) string {
	switch {
	case host == "":
		return "is empty: give a host or a socket directory"
	case len(host) > 1024 || strings.ContainsAny(host, "\x00\r\n ,"):
		return "is over 1024 bytes or holds a space, a comma or a line break, so it is not a host or a socket directory"
	case strings.Contains(host, "@") || strings.Contains(host, "://"):
		return "holds an @ or ://, as a URL or a login does; give the server's name or address alone, and the user and password in their own fields, where the password is kept secret"
	case path.IsAbs(host):
		// A socket directory, the one host that holds a slash.
		return ""
	case strings.Contains(host, "/"):
		return "holds a /, which only a socket directory, given as an absolute path, may"
	case strings.Contains(host, ":"):
		// Only an IPv6 address holds a colon; a port has its own field.
		if _, err := netip.ParseAddr(host); err != nil {
			return "holds a : and is not an IPv6 address; give the port in its own field, and an IPv6 address bare, without brackets"
		}
	}
	return ""
}

// IsLive reports whether the connection lists database as live.
func (c DatabaseConnection) IsLive(database string) bool {
	for _, db := range c.Live {
		if db == database {
			return true
		}
	}
	return false
}

// caProblem says why text cannot be a connection's CA, or "" when it can:
// PEM holding certificates only, at least one, each of which parses. It says
// nothing of the text itself, not even a block's type.
func caProblem(text string) string {
	rest := []byte(text)
	found := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return "holds a PEM block that is not a certificate; it may hold certificates only"
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return "holds a certificate that does not parse"
		}
		found++
	}
	if found == 0 {
		return "holds no PEM certificate"
	}
	return ""
}

// FindDatabaseConnection returns the connection named name in conns.
func FindDatabaseConnection(conns []DatabaseConnection, name string) (DatabaseConnection, bool) {
	for _, c := range conns {
		if c.Name == name {
			return c, true
		}
	}
	return DatabaseConnection{}, false
}

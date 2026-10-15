// A named database connection on its way to the one method that may use
// it.
package wire

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
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

// Validate refuses a connection no run could use as written.
func (c DatabaseConnection) Validate() error {
	switch {
	case !ValidDatabaseConnectionName(c.Name):
		return fmt.Errorf("%q is not a connection name (a lowercase letter, then up to 62 lowercase letters, digits, - and _)", c.Name)
	case c.Host == "" || strings.ContainsAny(c.Host, "\x00\r\n ,") || len(c.Host) > 1024:
		return fmt.Errorf("connection %s: %q is not a host or a socket directory", c.Name, c.Host)
	case c.Port < 0 || c.Port > 65535:
		return fmt.Errorf("connection %s: %d is not a port", c.Name, c.Port)
	case c.User == "" || strings.ContainsAny(c.User, "\x00\r\n"):
		return fmt.Errorf("connection %s: it needs a user", c.Name)
	}
	switch c.EffectiveSSLMode() {
	case SSLVerifyFull:
		if c.Socket() {
			return fmt.Errorf("connection %s: a socket carries no TLS; use sslmode %s", c.Name, SSLDisable)
		}
		if c.InsecureTransport {
			return fmt.Errorf("connection %s: insecure_transport allows nothing with sslmode %s; it is for %s to a host that is not local", c.Name, SSLVerifyFull, SSLDisable)
		}
	case SSLDisable:
		if c.CAPEM != "" || c.ServerName != "" {
			return fmt.Errorf("connection %s: a CA or server name means nothing with sslmode %s", c.Name, SSLDisable)
		}
		if c.Plaintext() && !c.InsecureTransport {
			return fmt.Errorf("connection %s: sslmode %s to %s would send the password and every row across the network in the clear; set insecure_transport to allow it, or use %s", c.Name, SSLDisable, c.Host, SSLVerifyFull)
		}
		if !c.Plaintext() && c.InsecureTransport {
			return fmt.Errorf("connection %s: insecure_transport allows nothing for a local server", c.Name)
		}
	default:
		return fmt.Errorf("connection %s: sslmode %q is not %s or %s", c.Name, c.SSLMode, SSLVerifyFull, SSLDisable)
	}
	if c.CAPEM != "" {
		if err := checkCAPEM(c.CAPEM); err != nil {
			return fmt.Errorf("connection %s: %w", c.Name, err)
		}
	}
	for _, db := range c.Live {
		if !databaseIdentPattern.MatchString(db) {
			return fmt.Errorf("connection %s: live database %q is not a database name", c.Name, db)
		}
	}
	return nil
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

// checkCAPEM refuses PEM holding anything but certificates, or none.
func checkCAPEM(text string) error {
	rest := []byte(text)
	found := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("the CA holds a %s block; it may hold certificates only", block.Type)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fmt.Errorf("the CA holds a certificate that does not parse: %w", err)
		}
		found++
	}
	if found == 0 {
		return errors.New("the CA holds no PEM certificate")
	}
	return nil
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

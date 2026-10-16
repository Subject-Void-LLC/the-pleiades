// Named database connections in the project's credential file, beside the
// devices' and the registries', in a namespace of their own.
package credential

import (
	"context"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// databaseEntry is one stored connection. Only the password is a secret,
// and only it is encrypted; the rest is what list-database-connections
// shows.
type databaseEntry struct {
	Host              string   `yaml:"host"`
	Port              int      `yaml:"port,omitempty"`
	User              string   `yaml:"user"`
	PasswordEncrypted string   `yaml:"password_encrypted,omitempty"`
	SSLMode           string   `yaml:"sslmode,omitempty"`
	CAPEM             string   `yaml:"ca_pem,omitempty"`
	ServerName        string   `yaml:"server_name,omitempty"`
	InsecureTransport bool     `yaml:"insecure_transport,omitempty"`
	Live              []string `yaml:"live,omitempty"`
}

// connection is the entry as a connection named name, without its
// password.
func (e databaseEntry) connection(name string) wire.DatabaseConnection {
	return wire.DatabaseConnection{
		Name: name, Host: e.Host, Port: e.Port, User: e.User, SSLMode: e.SSLMode, CAPEM: e.CAPEM,
		ServerName: e.ServerName, InsecureTransport: e.InsecureTransport, Live: append([]string(nil), e.Live...),
	}
}

// DatabaseStore resolves a named database connection. The Crawl tier's run
// hands it to every method through sdk.DatabaseConnections, by the name a
// task gives, so a stored password reaches only the method that names it.
type DatabaseStore interface {
	// LookupDatabase returns the connection named name, password included,
	// and false when there is none.
	LookupDatabase(ctx context.Context, name string) (wire.DatabaseConnection, bool, error)
}

// SaveDatabaseConnection stores conn, replacing one of the same name,
// with its password encrypted under the project's master key like every
// device credential. A connection no run could use is refused here, not
// at the run.
func SaveDatabaseConnection(dir string, key []byte, conn wire.DatabaseConnection) error {
	if err := conn.Validate(); err != nil {
		return err
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return fmt.Errorf("failed to initialize credential encryption: %w", err)
	}
	return updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		if doc.Databases == nil {
			doc.Databases = make(map[string]databaseEntry)
		}
		entry := databaseEntry{
			Host: conn.Host, Port: conn.Port, User: conn.User, SSLMode: conn.SSLMode, CAPEM: conn.CAPEM,
			ServerName: conn.ServerName, InsecureTransport: conn.InsecureTransport, Live: conn.Live,
		}
		if conn.Password != "" {
			encrypted, err := encryptField(svc, []byte(conn.Password))
			if err != nil {
				return fmt.Errorf("failed to encrypt the password of connection %s: %w", conn.Name, err)
			}
			entry.PasswordEncrypted = encrypted
		}
		doc.Databases[conn.Name] = entry
		return nil
	})
}

// RemoveDatabaseConnection removes the connection named name, reporting
// whether there was one.
func RemoveDatabaseConnection(dir, name string) (bool, error) {
	removed := false
	err := updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		if _, ok := doc.Databases[name]; ok {
			delete(doc.Databases, name)
			removed = true
		}
		return nil
	})
	return removed, err
}

// ListDatabaseConnections returns every stored connection, by name,
// without passwords: listing needs no master key and decrypts nothing.
func ListDatabaseConnections(dir string) ([]wire.DatabaseConnection, error) {
	doc, err := readCredentialsDocument(credentialsPath(dir))
	if err != nil {
		return nil, err
	}
	out := make([]wire.DatabaseConnection, 0, len(doc.Databases))
	for name, entry := range doc.Databases {
		out = append(out, entry.connection(name))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LookupDatabase implements DatabaseStore.
func (s *fileStore) LookupDatabase(_ context.Context, name string) (wire.DatabaseConnection, bool, error) {
	doc, err := readCredentialsDocument(s.path)
	if err != nil {
		return wire.DatabaseConnection{}, false, err
	}
	entry, ok := doc.Databases[name]
	if !ok {
		return wire.DatabaseConnection{}, false, nil
	}
	conn := entry.connection(name)
	if entry.PasswordEncrypted != "" {
		plain, err := s.decryptField(entry.PasswordEncrypted)
		if err != nil {
			return wire.DatabaseConnection{}, false, fmt.Errorf("failed to decrypt the password of connection %s: %w", name, err)
		}
		conn.Password = string(plain)
	}
	return conn, true, nil
}

// LookupDatabase implements DatabaseStore, resolving the master key on
// first use as Lookup does.
func (l *lazyFileStore) LookupDatabase(ctx context.Context, name string) (wire.DatabaseConnection, bool, error) {
	if err := l.open(); err != nil {
		return wire.DatabaseConnection{}, false, err
	}
	ds, ok := l.store.(DatabaseStore)
	if !ok {
		return wire.DatabaseConnection{}, false, nil
	}
	return ds.LookupDatabase(ctx, name)
}

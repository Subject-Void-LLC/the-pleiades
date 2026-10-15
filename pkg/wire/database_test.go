// Tests for which database connections are local, which cross a network
// in the clear, and the defaults.
package wire_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestDatabaseConnection_Local: a socket and every loopback spelling stay
// on the device; a name that merely starts like one does not.
func TestDatabaseConnection_Local(t *testing.T) {
	for host, local := range map[string]bool{
		"/var/run/postgresql": true, "localhost": true, "LOCALHOST": true, "127.0.0.1": true, "127.9.9.9": true, "::1": true,
		"localhost.example.com": false, "10.0.0.5": false, "db.internal": false, "::ffff:10.0.0.5": false, "relative/dir": false,
	} {
		c := wire.DatabaseConnection{Name: "x", Host: host, User: "u", SSLMode: wire.SSLDisable}
		if c.Local() != local || c.Plaintext() == local {
			t.Errorf("%s: Local %v, Plaintext %v", host, c.Local(), c.Plaintext())
		}
	}
	c := wire.DatabaseConnection{Name: "x", Host: "db", User: "u"}
	if c.EffectivePort() != 5432 || c.EffectiveSSLMode() != wire.SSLVerifyFull || c.Plaintext() || c.Validate() != nil {
		t.Errorf("defaults: %d %s %v %v", c.EffectivePort(), c.EffectiveSSLMode(), c.Plaintext(), c.Validate())
	}
	if _, ok := wire.FindDatabaseConnection([]wire.DatabaseConnection{c}, "x"); !ok {
		t.Error("FindDatabaseConnection missed x")
	}
	if _, ok := wire.FindDatabaseConnection([]wire.DatabaseConnection{c}, "y"); ok {
		t.Error("FindDatabaseConnection found y")
	}
}

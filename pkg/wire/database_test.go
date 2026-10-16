// Tests for which database connections are local, which cross a network
// in the clear, and the defaults.
package wire_test

import (
	"strings"
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

// TestDatabaseConnection_ValidatesTheHost: a name, an address, a bare
// IPv6 literal (zoned or not) and a socket directory are hosts. A URL, a
// login, a path that is not a socket directory, a port or a bracketed
// address written into the host are refused, and the refusal never
// repeats the host, since a URL or user:password@host carries a password
// that would then be stored and shown as an ordinary input.
func TestDatabaseConnection_ValidatesTheHost(t *testing.T) {
	const secret = "pw-in-the-host"
	for host, valid := range map[string]bool{
		"db":                  true,
		"db.internal":         true,
		"10.0.0.5":            true,
		"::1":                 true,
		"2001:db8::5":         true,
		"fe80::1%eth0":        true,
		"/var/run/postgresql": true,

		"postgres://app:" + secret + "@db/app": false,
		"postgresql://db:5432/app":             false,
		"app:" + secret + "@db":                false,
		"app@db":                               false,
		"/run/app:" + secret + "@db":           false,
		"/tmp/x://y":                           false,
		"db/app":                               false,
		"db:5432":                              false,
		"app:" + secret:                        false,
		"[::1]":                                false,
		"[::1]:5432":                           false,
		"":                                     false,
		"db " + secret:                         false,
	} {
		c := wire.DatabaseConnection{Name: "x", Host: host, User: "u"}
		// A socket carries no TLS, so a socket directory is tried without it.
		if strings.HasPrefix(host, "/") {
			c.SSLMode = wire.SSLDisable
		}
		err := c.Validate()
		if valid != (err == nil) {
			t.Errorf("Validate(%q) = %v, want valid %v", host, err, valid)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("Validate(%q) repeats the password: %v", host, err)
		}
	}
}

// Package remoteexec: naming what a refused host key was. knownhosts says
// "key mismatch" or "key is unknown" and no more, which leaves an operator
// whose device made a new key at boot (a Catalyst 8000V on 17.15 makes a new
// port 22 key at every boot, saved configuration or not) with nothing to check
// the new key against. The refusal itself is unchanged: a changed key stays
// refused, as a machine in the middle presents one too. Only its words grow.
package remoteexec

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// HostKeyError is a host key known_hosts refused, with what was presented and
// what was known. It wraps the library's own error, so errors.As still finds
// a *knownhosts.KeyError or *knownhosts.RevokedError under it.
type HostKeyError struct {
	// Addr is the address the key was presented at, host and port.
	Addr string
	// Presented is the key the server presented.
	Presented ssh.PublicKey
	// Known is every key known_hosts trusts for Addr, with its file and line;
	// empty when the address has none (an unknown host).
	Known []knownhosts.KnownKey
	// Revoked is set when known_hosts marks Presented revoked.
	Revoked bool
	err     error
}

// Fingerprint is a key's SHA-256 fingerprint as OpenSSH prints it
// ("SHA256:" and the unpadded base64 of the digest), so it can be compared
// with ssh-keygen -lf and with what a device itself shows.
func Fingerprint(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Changed reports whether the address has keys in known_hosts and Presented
// is none of them: the host's key changed, or something else answered for it.
func (e *HostKeyError) Changed() bool { return !e.Revoked && len(e.Known) > 0 }

// Port is the port of Addr, "22" when Addr names none.
func (e *HostKeyError) Port() string {
	if _, port, err := net.SplitHostPort(e.Addr); err == nil {
		return port
	}
	return "22"
}

// Error names the address and port, the presented key's type and
// fingerprint, and for a changed key each trusted one with its file and line,
// then the library's own words.
func (e *HostKeyError) Error() string {
	host := e.Addr
	if h, _, err := net.SplitHostPort(e.Addr); err == nil {
		host = h
	}
	presented := e.Presented.Type() + " " + Fingerprint(e.Presented)
	switch {
	case e.Revoked:
		return fmt.Sprintf("the SSH host key at %s port %s, %s, is revoked in known_hosts, so nothing was sent: %v", host, e.Port(), presented, e.err)
	case len(e.Known) == 0:
		return fmt.Sprintf("the SSH host key at %s port %s is not in known_hosts: it presented %s, and no credential was sent to a host known_hosts does not name: %v", host, e.Port(), presented, e.err)
	}
	known := make([]string, 0, len(e.Known))
	for _, k := range e.Known {
		known = append(known, fmt.Sprintf("%s %s (%s:%d)", k.Key.Type(), Fingerprint(k.Key), k.Filename, k.Line))
	}
	return fmt.Sprintf("the SSH host key at %s port %s changed: it presented %s, and known_hosts trusts %s for it; a changed key is refused, since a machine in the middle presents one too, and no credential was sent: %v",
		host, e.Port(), presented, strings.Join(known, " and "), e.err)
}

// Unwrap exposes the library's own error.
func (e *HostKeyError) Unwrap() error { return e.err }

// describeHostKeyRefusal returns cb with every refusal it makes of a host key
// turned into a HostKeyError naming that key. It refuses exactly what cb
// refuses and accepts exactly what cb accepts; nil stays nil.
func describeHostKeyRefusal(cb ssh.HostKeyCallback) ssh.HostKeyCallback {
	if cb == nil {
		return nil
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		var keyErr *knownhosts.KeyError
		var revoked *knownhosts.RevokedError
		switch {
		case err == nil:
			return nil
		case errors.As(err, &keyErr):
			return &HostKeyError{Addr: hostname, Presented: key, Known: keyErr.Want, err: err}
		case errors.As(err, &revoked):
			return &HostKeyError{Addr: hostname, Presented: key, Revoked: true, err: err}
		}
		return err
	}
}

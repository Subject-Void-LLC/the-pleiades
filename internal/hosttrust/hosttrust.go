// Package hosttrust writes a device's SSH host keys into the known_hosts
// file every SSH connection Pleiades makes verifies against (Merge), and
// reads the keys a host presents on the network for an operator who
// chooses to trust them on first use (Scan).
//
// Where the keys come from is the caller's choice and the whole of the
// security question: keys a VM printed on its console and read over an
// authenticated channel to its host prove whose they are, while keys
// scanned off the network prove only who answered first.
package hosttrust

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Outcome is what Merge did to the file.
type Outcome struct {
	// Added is how many keys were written; Known how many were there
	// already; Removed how many lines naming the host --replace dropped.
	Added, Known, Removed int
}

// Merge makes the known_hosts file at path trust keys for address, a
// host with an optional :port. A key already trusted for it is left as
// it is. A host the file trusts under other keys is refused unless
// replace is set, since a host whose keys changed is also what a machine
// in the middle looks like; replace drops the file's lines naming it
// first. The file is written whole and renamed into place, readable by
// its owner only when Merge creates it.
func Merge(path, address string, keys []ssh.PublicKey, replace bool) (Outcome, error) {
	var out Outcome
	if len(keys) == 0 {
		return out, errors.New("hosttrust: no keys to trust")
	}
	host := knownhosts.Normalize(address)
	existing, mode, err := readFile(path)
	if err != nil {
		return out, err
	}
	var fresh []ssh.PublicKey
	for _, key := range keys {
		known, conflict, err := check(path, existing, address, key)
		if err != nil {
			return Outcome{}, err
		}
		switch {
		case known:
			out.Known++
		case conflict && !replace:
			return Outcome{}, fmt.Errorf("hosttrust: %s already trusts other keys for %s; if its keys changed on purpose (a VM made again), trust the new ones with --replace", path, host)
		default:
			fresh = append(fresh, key)
		}
	}
	lines := strings.SplitAfter(string(existing), "\n")
	if replace && len(fresh) > 0 {
		kept := lines[:0]
		for _, line := range lines {
			if names(line, host) {
				out.Removed++
				continue
			}
			kept = append(kept, line)
		}
		lines = kept
	}
	if len(fresh) == 0 {
		return out, nil
	}
	var b bytes.Buffer
	for _, line := range lines {
		b.WriteString(line)
	}
	if b.Len() > 0 && !bytes.HasSuffix(b.Bytes(), []byte("\n")) {
		b.WriteByte('\n')
	}
	for _, key := range fresh {
		b.WriteString(knownhosts.Line([]string{host}, key) + "\n")
		out.Added++
	}
	return out, writeFile(path, b.Bytes(), mode)
}

// Remove drops every line of the known_hosts file at path naming address
// (a host with an optional :port) exactly, returning how many it dropped. A
// file with none, or none at all, is left as it is.
func Remove(path, address string) (int, error) {
	host := knownhosts.Normalize(address)
	existing, mode, err := readFile(path)
	if err != nil || len(existing) == 0 {
		return 0, err
	}
	var b bytes.Buffer
	removed := 0
	for _, line := range strings.SplitAfter(string(existing), "\n") {
		if names(line, host) {
			removed++
			continue
		}
		b.WriteString(line)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, writeFile(path, b.Bytes(), mode)
}

// check reports whether the file already trusts key for address, and
// whether it trusts other keys for it instead. A file that does not parse
// is refused: every connection verifying against it fails, and adding to
// it would hide that.
func check(path string, content []byte, address string, key ssh.PublicKey) (known, conflict bool, err error) {
	if len(content) == 0 {
		return false, false, nil
	}
	callback, err := knownhosts.New(path)
	if err != nil {
		return false, false, fmt.Errorf("hosttrust: %s does not parse, so no connection can verify against it: %w", path, err)
	}
	host, port, err := net.SplitHostPort(withPort(address))
	if err != nil {
		return false, false, fmt.Errorf("hosttrust: %q is not host or host:port", address)
	}
	remote := &net.TCPAddr{IP: net.ParseIP(host)}
	err = callback(net.JoinHostPort(host, port), remote, key)
	var keyErr *knownhosts.KeyError
	switch {
	case err == nil:
		return true, false, nil
	case errors.As(err, &keyErr):
		return false, len(keyErr.Want) > 0, nil
	}
	return false, false, fmt.Errorf("hosttrust: checking %s: %w", path, err)
}

// names reports whether a known_hosts line names host exactly, among its
// comma-separated host patterns.
func names(line, host string) bool {
	fields := strings.Fields(line)
	if len(fields) < 3 || strings.HasPrefix(fields[0], "#") {
		return false
	}
	patterns := fields[0]
	if strings.HasPrefix(patterns, "@") && len(fields) > 3 {
		patterns = fields[1]
	}
	for _, p := range strings.Split(patterns, ",") {
		if p == host {
			return true
		}
	}
	return false
}

// withPort adds SSH's port to an address that names none.
func withPort(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, "22")
}

// readFile returns the file's content and mode, or nothing and 0600 for
// a file that does not exist yet.
func readFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0o600, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("hosttrust: %w", err)
	}
	content, err := os.ReadFile(path) // #nosec G304 -- the known_hosts file this process's own SSH connections verify against
	if err != nil {
		return nil, 0, fmt.Errorf("hosttrust: %w", err)
	}
	return content, info.Mode().Perm(), nil
}

// writeFile writes content to path through a file beside it, renamed into
// place, creating the folder (owner only) when it is missing.
func writeFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("hosttrust: %w", err)
	}
	part, err := os.CreateTemp(filepath.Dir(path), ".known_hosts-*")
	if err != nil {
		return fmt.Errorf("hosttrust: %w", err)
	}
	defer os.Remove(part.Name())
	if _, err := part.Write(content); err != nil {
		return errors.Join(fmt.Errorf("hosttrust: %w", err), part.Close())
	}
	if err := part.Chmod(mode); err != nil {
		return errors.Join(fmt.Errorf("hosttrust: %w", err), part.Close())
	}
	if err := part.Close(); err != nil {
		return fmt.Errorf("hosttrust: %w", err)
	}
	if err := os.Rename(part.Name(), path); err != nil {
		return fmt.Errorf("hosttrust: %w", err)
	}
	return nil
}

// errSeen ends a handshake once the host has shown its key.
var errSeen = errors.New("hosttrust: key seen")

// Scan connects to address once per kind of host key and returns the keys
// the host presents, without logging in, negotiating only what legacy (the
// device record's legacy SSH algorithms) allows, as every connection to it
// does (pkg/remoteexec's legacy.go). Nothing proves the host at address is
// the one meant: whatever answers first is what is returned.
//
// A host that offers only legacy algorithms its record does not allow is
// refused naming them and the property that allows them, rather than said
// to present no key: an IOSvL2 15.2 switch offers SHA-1 key exchange and an
// ssh-rsa host key and nothing else.
func Scan(ctx context.Context, address string, timeout time.Duration, legacy remoteexec.Legacy) ([]ssh.PublicKey, error) {
	algorithms, groups := legacy.ScanConfig()
	var keys []ssh.PublicKey
	var last, refused error
	for _, group := range groups {
		key, err := scanOne(ctx, withPort(address), algorithms, group, timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = err
			// A key exchange with nothing in common refuses every group the
			// same way, and a host key group the host has no key for is
			// expected; a refusal naming the property that would allow the
			// host's offer is the one worth reporting, the first one kept.
			candidate := remoteexec.ExplainNegotiation(err, withPort(address), legacy)
			if candidate != err && (refused == nil || namesProperty(candidate) && !namesProperty(refused)) {
				refused = candidate
			}
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		// A host that refused or never answered the connection is said to
		// be unreachable: "presented no host key" sent a reader looking at
		// SSH algorithms for a router that runs no SSH server at all.
		var op *net.OpError
		switch {
		case errors.As(last, &op) && op.Op == "dial":
			return nil, fmt.Errorf("hosttrust: %s could not be reached over SSH: %w", address, last)
		case refused != nil && namesProperty(refused):
			return nil, fmt.Errorf("hosttrust: %w", refused)
		}
		return nil, fmt.Errorf("hosttrust: %s presented no host key: %w", address, last)
	}
	return keys, nil
}

// namesProperty reports whether err is a legacy refusal naming the property
// that would allow what the host offered.
func namesProperty(err error) bool {
	var named *remoteexec.LegacyRefusedError
	return errors.As(err, &named) && named.Property != ""
}

// scanOne returns the key address presents for the first of hostKeys it
// shares with the host, negotiating algorithms for everything else.
func scanOne(ctx context.Context, address string, algorithms ssh.Config, hostKeys []string, timeout time.Duration) (ssh.PublicKey, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	var seen ssh.PublicKey
	config := &ssh.ClientConfig{
		Config:            algorithms,
		User:              "pleiades-trust-host",
		HostKeyAlgorithms: hostKeys,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = key
			return errSeen
		},
		Timeout: timeout,
	}
	_, _, _, err = ssh.NewClientConn(conn, address, config)
	if seen != nil {
		return seen, nil
	}
	return nil, err
}

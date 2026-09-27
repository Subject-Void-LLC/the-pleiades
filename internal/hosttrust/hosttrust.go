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

// scanAlgorithms are the host key algorithms Scan asks for one at a time,
// so it sees each kind of key a host has, as ssh-keyscan does.
var scanAlgorithms = []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512}

// errSeen ends a handshake once the host has shown its key.
var errSeen = errors.New("hosttrust: key seen")

// Scan connects to address once per host key algorithm and returns the
// keys the host presents, without logging in. Nothing proves the host at
// address is the one meant: whatever answers first is what is returned.
func Scan(ctx context.Context, address string, timeout time.Duration) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for _, algorithm := range scanAlgorithms {
		key, err := scanOne(ctx, withPort(address), algorithm, timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("hosttrust: %s presented no host key", address)
	}
	return keys, nil
}

// scanOne returns the key address presents for algorithm.
func scanOne(ctx context.Context, address, algorithm string, timeout time.Duration) (ssh.PublicKey, error) {
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
		User:              "pleiades-trust-host",
		HostKeyAlgorithms: []string{algorithm},
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

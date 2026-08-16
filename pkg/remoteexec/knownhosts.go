package remoteexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback builds the ssh.HostKeyCallback one connection uses to
// verify its target's host key, per opts.
//
// Host key verification is what stops a machine in the middle answering
// for the device, so every branch here fails closed.
// InsecureSkipHostKeyVerify is the only way to bypass it, and it must be
// set explicitly and loudly by the caller. A missing known_hosts file is
// never treated as trust on first use: it is a hard error, mirroring
// `ssh -o StrictHostKeyChecking=yes` against an unpopulated file. A host
// whose key does not match its entry, or that has no entry at all in a
// file that does exist, is rejected by the knownhosts callback itself
// with a clear, actionable error.
//
// This runs fresh for every connection rather than once when a Runner is
// built. That keeps New free of an error return, means a known_hosts
// file populated after construction still works, and is what lets
// Shared memoize a Runner without also freezing a $HOME that a later
// caller may have changed. The repeated stat and parse cost is nothing
// next to a real network round trip.
func hostKeyCallback(opts Options) (ssh.HostKeyCallback, error) {
	if opts.InsecureSkipHostKeyVerify {
		// Reachable only through this explicit, loud opt-in. Never the
		// default, and never silently substituted for a broken
		// known_hosts source below.
		return ssh.InsecureIgnoreHostKey(), nil // #nosec G106 -- explicit opt-in only, see above
	}

	path, err := knownHostsPath(opts)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fail closed: a missing known_hosts file must never become
			// "trust everything."
			return nil, fmt.Errorf("known_hosts file %q not found, host key verification cannot proceed (set InsecureSkipHostKeyVerify to explicitly bypass this)", path)
		}
		return nil, fmt.Errorf("stat known_hosts file %q: %w", path, err)
	}

	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("parse known_hosts file %q: %w", path, err)
	}
	return cb, nil
}

// knownHostsPath returns the known_hosts file to verify against: the
// caller's explicit path, or "$HOME/.ssh/known_hosts" when none is set.
//
// It resolves $HOME here, per connection, rather than once when a Runner
// is built. $HOME may also be unavailable in a sandboxed environment, in
// which case this reports that plainly instead of producing a path that
// cannot exist and letting the stat above blame a missing file.
func knownHostsPath(opts Options) (string, error) {
	if opts.KnownHostsPath != "" {
		return opts.KnownHostsPath, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no known_hosts path configured and the home directory could not be determined, host key verification cannot proceed: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}
